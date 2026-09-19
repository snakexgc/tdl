package downloadtrigger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
	"github.com/snakexgc/tdl/rte/eventbus"
)

const ID = "trigger.download"

func Register(registry *rte.Registry, handler ports.DownloadIntentHandler, capacity int, results ...ports.DownloadIntentResultHandler) error {
	if handler == nil || capacity < 1 {
		return errors.New("download intents require a handler and positive capacity")
	}
	var resultHandler ports.DownloadIntentResultHandler
	if len(results) > 0 {
		resultHandler = results[0]
	}
	provides := []manifest.Port{manifest.PortOf[ports.DownloadIntents](ports.DownloadIntentsName, 1, 0)}
	if resultHandler != nil {
		provides = append(provides, manifest.PortOf[ports.DownloadRequests](ports.DownloadRequestsName, 1, 0))
	}
	m := Manifest()
	m.Provides = provides
	return registry.Register(m, func() rte.Component {
		return &service{handler: handler, resultHandler: resultHandler, capacity: capacity, pending: map[string]intentCall{}}
	})
}

func Manifest() manifest.Manifest {
	return manifest.Manifest{
		ID: ID, Title: "下载触发意图",
		Provides:  []manifest.Port{manifest.PortOf[ports.DownloadIntents](ports.DownloadIntentsName, 1, 0)},
		Publishes: []string{types.DownloadRequested}, Subscribes: []string{types.DownloadRequested},
	}
}

type service struct {
	ctx           context.Context
	mu            sync.Mutex
	sequence      uint64
	pending       map[string]intentCall
	resultHandler ports.DownloadIntentResultHandler
	account       types.AccountID
	events        rte.Events
	handler       ports.DownloadIntentHandler
	capacity      int
}

type intentResponse struct {
	result types.DownloadSubmissionSummary
	err    error
}

type intentCall struct {
	ctx   context.Context
	reply chan intentResponse
}

func (s *service) Init(ctx context.Context, k rte.Kernel) error {
	s.ctx = ctx
	s.account, s.events = k.Account, k.Events
	if _, err := k.Events.Subscribe(types.DownloadRequested, s.capacity, func(ctx context.Context, event eventbus.Event) error {
		var request types.DownloadIntent
		if err := json.Unmarshal(event.Payload, &request); err != nil {
			return err
		}
		if request.Account != event.Account {
			return errors.New("download intent account mismatch")
		}
		if s.resultHandler == nil {
			return s.handler(ctx, request)
		}
		var call intentCall
		if request.RequestID != "" {
			s.mu.Lock()
			call = s.pending[request.RequestID]
			s.mu.Unlock()
			if call.reply == nil || call.ctx.Err() != nil {
				return nil
			}
			linked, cancel := context.WithCancel(ctx)
			unlink := context.AfterFunc(call.ctx, cancel)
			defer func() { unlink(); cancel() }()
			if call.ctx.Err() != nil {
				cancel()
			}
			ctx = linked
		}
		result, err := invokeResult(s.resultHandler, ctx, request)
		if call.reply != nil {
			call.reply <- intentResponse{result: result, err: err}
		}
		return err
	}, nil); err != nil {
		return err
	}
	if s.resultHandler != nil {
		if err := k.Provide(ports.DownloadRequestsName, s); err != nil {
			return err
		}
	}
	return k.Provide(ports.DownloadIntentsName, s)
}

func invokeResult(handler ports.DownloadIntentResultHandler, ctx context.Context, request types.DownloadIntent) (result types.DownloadSubmissionSummary, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("download handler panic: %v", recovered)
		}
	}()
	return handler(ctx, request)
}

func (s *service) Publish(ctx context.Context, request types.DownloadIntent) error {
	request.RequestID = ""
	return s.publish(ctx, request)
}

func (s *service) publish(ctx context.Context, request types.DownloadIntent) error {
	if request.Account != s.account {
		return errors.New("download intent account mismatch")
	}
	if request.MessageID <= 0 {
		return errors.New("download intent requires a message ID")
	}
	return s.events.Publish(ctx, types.DownloadRequested, request)
}

func (s *service) Submit(ctx context.Context, request types.DownloadIntent) (types.DownloadSubmissionSummary, error) {
	if err := ctx.Err(); err != nil {
		return types.DownloadSubmissionSummary{}, err
	}
	if s.resultHandler == nil {
		return types.DownloadSubmissionSummary{}, errors.New("download results are unavailable")
	}
	s.mu.Lock()
	if len(s.pending) >= s.capacity {
		s.mu.Unlock()
		return types.DownloadSubmissionSummary{}, eventbus.ErrFull
	}
	s.sequence++
	request.RequestID = strconv.FormatUint(s.sequence, 10)
	reply := make(chan intentResponse, 1)
	s.pending[request.RequestID] = intentCall{ctx: ctx, reply: reply}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.pending, request.RequestID); s.mu.Unlock() }()
	if err := s.publish(ctx, request); err != nil {
		return types.DownloadSubmissionSummary{}, err
	}
	select {
	case response := <-reply:
		return response.result, response.err
	case <-ctx.Done():
		return types.DownloadSubmissionSummary{}, ctx.Err()
	case <-s.ctx.Done():
		return types.DownloadSubmissionSummary{}, s.ctx.Err()
	}
}
func (*service) Start(context.Context) error                    { return nil }
func (*service) Stop(context.Context) error                     { return nil }
func (*service) Reconfigure(context.Context, config.View) error { return nil }
