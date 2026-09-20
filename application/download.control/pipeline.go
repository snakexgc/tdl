package downloadcontrol

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte/targetpath"
)

type downloadPlan struct {
	media                   ports.DownloadMedia
	local, remote           ports.NamingResult
	localError, remoteError error
	fileName                string
}

type submissionOutcome struct {
	result types.DownloadResult
	link   types.GeneratedDownloadLink
	err    error
}

// SubmitBatch owns collection, policy selection, preparation and admission.
// Returning means every submission has finished; it does not wait for transfer
// completion. A connection can safely drain this call before closing its pool.
func (s *Service) SubmitBatch(ctx context.Context, request types.DownloadIntent, resources ports.DownloadResources) (result types.DownloadSubmissionSummary, resultErr error) {
	result = types.DownloadSubmissionSummary{Link: request.Link, PeerID: request.PeerID, MessageID: request.MessageID}
	ctx, done, err := s.begin(ctx)
	if err != nil {
		return result, err
	}
	defer done()
	if request.Account != s.account || request.MessageID <= 0 {
		return result, fmt.Errorf("invalid download intent account or message")
	}
	started := time.Now()
	defer func() {
		level := slog.LevelInfo
		if resultErr != nil || result.Failed > 0 || result.Uncertain > 0 {
			level = slog.LevelError
		}
		if ctx.Err() != nil || errors.Is(resultErr, context.Canceled) {
			level = slog.LevelDebug
		}
		slog.Log(ctx, level, "下载提交已结束", "component", ID, "account", s.account,
			"peer_id", request.PeerID, "message_id", request.MessageID, "total", result.Total,
			"queued", result.Queued, "skipped", result.Skipped, "failed", result.Failed,
			"uncertain", result.Uncertain, "duration", time.Since(started), "error", resultErr)
	}()
	if resources.Source == nil || resources.Filter == nil || resources.Naming == nil || resources.Files == nil {
		return result, fmt.Errorf("download pipeline resources are unavailable")
	}
	route, err := s.batchRoute(ctx, resources)
	if err != nil {
		return result, err
	}
	media, registration, err := resources.Source.Collect(ctx, request)
	if err != nil {
		return result, err
	}
	result.Total = len(media)
	if len(media) == 0 {
		return result, nil
	}
	if registration == nil {
		result.Failed = len(media)
		return result, fmt.Errorf("download source registration is unavailable")
	}
	plans, preparationError := s.prepareBatch(ctx, media, route, resources, &result)
	outcomes := make([]submissionOutcome, len(plans))
	limit := max(1, resources.Defaults.Limit)
	indexes := make(chan int, len(plans))
	for index := range plans {
		indexes <- index
	}
	close(indexes)
	var workers sync.WaitGroup
	for range min(limit, len(plans)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range indexes {
				outcomes[index] = s.submitPrepared(ctx, plans[index], route, resources, registration)
			}
		}()
	}
	workers.Wait()
	for index, outcome := range outcomes {
		switch {
		case outcome.result.Skipped:
			result.Skipped++
		case outcome.err == nil || outcome.result.ID != "":
			result.Queued++
		case errors.Is(outcome.err, ports.ErrDownloadNotAccepted):
			result.Failed++
		default:
			result.Uncertain++
		}
		if outcome.link.URL != "" {
			result.Links = append(result.Links, outcome.link)
		}
		if outcome.err != nil {
			preparationError = errors.Join(preparationError, fmt.Errorf("message %d (%s): %w", plans[index].media.Data.MessageID, plans[index].media.Data.FileName, outcome.err))
		}
	}
	return result, preparationError
}

func (s *Service) batchRoute(ctx context.Context, r ports.DownloadResources) (ports.DownloadRoute, error) {
	var route ports.DownloadRoute
	var err error
	if r.Routing != nil {
		route, err = r.Routing.Route(ctx, s.account)
	} else {
		route, err = s.Route(ctx, s.account)
	}
	if err != nil {
		return route, err
	}
	route.Executors = slices.Clone(route.Executors)
	if len(route.Executors) == 0 {
		return route, fmt.Errorf("download executor list is empty")
	}
	if slices.Contains(route.Executors, localExecutor) && !filepath.IsAbs(route.LocalRoot) {
		return route, fmt.Errorf("local download root must be absolute")
	}
	return route, nil
}

func (s *Service) prepareBatch(ctx context.Context, media []ports.DownloadMedia, route ports.DownloadRoute, r ports.DownloadResources, summary *types.DownloadSubmissionSummary) ([]downloadPlan, error) {
	plans := make([]downloadPlan, 0, len(media))
	var failures error
	for _, item := range media {
		if err := ctx.Err(); err != nil {
			summary.Failed++
			failures = errors.Join(failures, err)
			continue
		}
		allowed, reason := r.Filter.ShouldHandle(ctx, ports.FilterInput{Account: s.account, Name: item.Data.FileName, Size: item.Data.FileSize})
		if !allowed {
			slog.Debug("下载媒体未通过筛选", "component", ID, "account", s.account, "message_id", item.Data.MessageID, "reason", reason)
			if reason == ports.ExtensionExcluded || reason == ports.SizeExcluded {
				summary.Skipped++
			} else {
				summary.Failed++
				failures = errors.Join(failures, fmt.Errorf("filter unavailable for message %d: %s", item.Data.MessageID, reason))
			}
			continue
		}
		if item.Token == "" || item.Data.MessageID <= 0 || item.Data.FileSize < 0 {
			summary.Failed++
			failures = errors.Join(failures, fmt.Errorf("invalid download media metadata"))
			continue
		}
		plan := downloadPlan{media: item}
		if slices.Contains(route.Executors, localExecutor) {
			plan.local, plan.localError = r.Naming.Render(ctx, ports.NamingInput{Account: s.account, BaseDir: route.LocalRoot, Data: item.Data})
		}
		if slices.Contains(route.Executors, aria2Executor) || slices.Contains(route.Executors, httpExecutor) {
			plan.remote, plan.remoteError = r.Naming.Render(ctx, ports.NamingInput{Account: s.account, BaseDir: targetpath.CleanTargetRoot(r.Defaults.RemoteRoot), Data: item.Data})
		}
		plan.fileName = plan.local.FileName
		if plan.fileName == "" {
			plan.fileName = plan.remote.FileName
		}
		if plan.fileName == "" {
			summary.Failed++
			failures = errors.Join(failures, fmt.Errorf("prepare message %d: %w", item.Data.MessageID, errors.Join(plan.localError, plan.remoteError)))
			continue
		}
		plans = append(plans, plan)
	}
	if !slices.Contains(route.Executors, localExecutor) {
		return plans, failures
	}
	var targets []ports.NamingResult
	var indexes []int
	for index, plan := range plans {
		if plan.localError == nil {
			targets, indexes = append(targets, plan.local), append(indexes, index)
		}
	}
	if len(targets) == 0 {
		return plans, failures
	}
	unique, err := r.Naming.Unique(ctx, targets)
	if err == nil && len(unique) != len(targets) {
		err = fmt.Errorf("naming returned an invalid target count")
	}
	for index, planIndex := range indexes {
		if err != nil {
			plans[planIndex].localError = err
		} else {
			plans[planIndex].local = unique[index]
			plans[planIndex].fileName = unique[index].FileName
		}
	}
	return plans, failures
}

func (s *Service) submitPrepared(ctx context.Context, plan downloadPlan, route ports.DownloadRoute, r ports.DownloadResources, source ports.DownloadRegistration) (out submissionOutcome) {
	// Preserve partial results if an adapter panics. After entering an executor
	// acceptance is uncertain and must not be retried through another backend.
	enteredExecutor := false
	defer func() {
		if recovered := recover(); recovered != nil {
			out.err = fmt.Errorf("download adapter panic: %v", recovered)
			if !enteredExecutor {
				out.err = errors.Join(out.err, ports.ErrDownloadNotAccepted)
			}
		}
	}()
	if err := ctx.Err(); err != nil {
		out.err = errors.Join(err, ports.ErrDownloadNotAccepted)
		return out
	}
	id, err := source.Register(ctx, plan.media.Token, plan.fileName)
	if err != nil || id == "" {
		out.err = fmt.Errorf("register download source: %v: %w", err, ports.ErrDownloadNotAccepted)
		return out
	}
	var downloadURL string
	var urlError error
	if slices.Contains(route.Executors, aria2Executor) || slices.Contains(route.Executors, httpExecutor) {
		downloadURL, urlError = source.URL(ctx, id)
	}
	executors := make([]ports.DownloadExecutor, 0, len(route.Executors))
	for _, name := range route.Executors {
		executors = append(executors, batchExecutor{name: name, submit: func(call context.Context, in types.DownloadSubmission) (types.DownloadResult, error) {
			if name == localExecutor {
				if plan.localError != nil {
					return types.DownloadResult{}, rejectedTarget(plan.localError)
				}
				if r.Defaults.SkipSame {
					same, err := r.Files.SameFile(call, plan.local.FullPath, plan.media.Data.FileSize)
					if err != nil {
						return types.DownloadResult{}, rejectedTarget(err)
					}
					if same {
						return types.DownloadResult{Account: s.account, Target: name, ID: id, Skipped: true}, nil
					}
				}
				if r.Executors[name] == nil {
					return types.DownloadResult{}, ports.ErrDownloadNotAccepted
				}
				if err := r.Files.EnsureDirectory(call, plan.local.Dir); err != nil {
					return types.DownloadResult{}, rejectedTarget(err)
				}
				in.Dir, in.Out, in.FullPath = plan.local.Dir, plan.local.Out, plan.local.FullPath
			} else {
				if urlError != nil || downloadURL == "" {
					return types.DownloadResult{}, rejectedTarget(fmt.Errorf("download URL unavailable: %v", urlError))
				}
				if name == httpExecutor {
					return (LinkExecutor{}).Submit(call, in)
				}
				if plan.remoteError != nil {
					return types.DownloadResult{}, rejectedTarget(plan.remoteError)
				}
				in.Dir, in.Out, in.FullPath = plan.remote.Dir, plan.remote.Out, plan.remote.FullPath
			}
			if executor := r.Executors[name]; executor != nil {
				if err := call.Err(); err != nil {
					return types.DownloadResult{}, errors.Join(err, ports.ErrDownloadNotAccepted)
				}
				enteredExecutor = true
				result, err := executor.Submit(call, in)
				if errors.Is(err, ports.ErrDownloadNotAccepted) && result.ID == "" {
					enteredExecutor = false
				}
				return result, err
			}
			return types.DownloadResult{}, ports.ErrDownloadNotAccepted
		}})
	}
	out.result, out.err = NewRouter(s.account, executors...).Submit(ctx, types.DownloadSubmission{Account: s.account, TaskID: id, DownloadURL: downloadURL})
	if out.err != nil && !enteredExecutor {
		out.err = errors.Join(out.err, ports.ErrDownloadNotAccepted)
	}
	if out.result.Target == httpExecutor && out.err == nil {
		out.link = types.GeneratedDownloadLink{FileName: plan.fileName, URL: downloadURL}
	}
	return out
}

func rejectedTarget(err error) error {
	return fmt.Errorf("download target unavailable: %v: %w", err, ports.ErrDownloadNotAccepted)
}

type batchExecutor struct {
	name   string
	submit func(context.Context, types.DownloadSubmission) (types.DownloadResult, error)
}

func (e batchExecutor) Name() string { return e.name }
func (e batchExecutor) Submit(ctx context.Context, in types.DownloadSubmission) (types.DownloadResult, error) {
	return e.submit(ctx, in)
}
