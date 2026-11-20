// Package downloadlisten owns listener rules, durable admission and bounded reconciliation.
package downloadlisten

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

const (
	ID             = "download.listen"
	scanInterval   = 5 * time.Minute
	recoveryWindow = 24 * time.Hour
)

type policy struct {
	rules    []types.DownloadListenRule
	comments map[types.ChatRef]bool
	record   types.ListenPolicyRecord
}

type Service struct {
	mu           sync.Mutex
	serveMu      sync.Mutex
	repository   ports.ListenRepository
	account      types.AccountID
	ctx          context.Context
	cancel       context.CancelFunc
	clock        func() time.Time
	current      *policy
	prepared     *policy
	state        types.ListenState
	wake         chan struct{}
	active       map[string]bool
	connected    bool
	enabled      func() bool
	blockedUntil time.Time
	lastError    string
	filters      types.ListenFilters
	closed       bool
	serving      sync.WaitGroup
}

func Manifest() manifest.Manifest {
	return manifest.Manifest{
		ID: ID, Title: "监听下载", Feature: manifest.Feature{ID: "download", Title: "下载管理", Order: 10, SettingsURL: pagePath},
		Pages:    []manifest.Page{{Path: pagePath, Title: "监听下载", View: "listen-downloads", Module: "/static/js/listen-downloads.js", Style: "/static/css/listen-downloads.css", Order: 25, KeepVisible: true, SettingsURL: pagePath}},
		Config:   []manifest.ConfigField{{Name: rulesField, Title: "监听规则", Help: "在监听下载页面选择来源，保存后立即生效。", Type: manifest.Objects, Default: []types.DownloadListenRule{}, Editor: "/static/js/listen-rules.js", SettingsTab: "download", SettingsSection: "自动监听", SettingsOrder: 60}},
		Provides: []manifest.Port{manifest.PortOf[ports.DownloadListener](ports.DownloadListenName, 1, 0), manifest.PortOf[ports.WebAction](ports.DownloadListenStatusName, 1, 0)},
	}
}

func Register(registry *rte.Registry, repository ports.ListenRepository, filters types.ListenFilters) error {
	return registry.Register(Manifest(), func() rte.Component { return New(repository, filters) })
}

func New(repository ports.ListenRepository, filters types.ListenFilters) *Service {
	return &Service{
		repository: repository, filters: filters, enabled: func() bool { return filters.DownloadEnabled }, wake: make(chan struct{}, 1), active: map[string]bool{}, clock: time.Now,
		state: types.ListenState{Sources: map[types.ChatRef]types.ListenSourceRecord{}, Threads: map[string]types.ListenThreadRecord{}, Jobs: map[string]types.ListenJobRecord{}},
	}
}

func (s *Service) Init(ctx context.Context, k rte.Kernel) error {
	s.account = k.Account
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.clock = func() time.Time { return rte.Now(ctx) }
	if s.repository != nil {
		state, err := s.repository.Load(ctx)
		if err != nil {
			return err
		}
		s.state = state
	}
	if err := s.Reconfigure(ctx, k.Config); err != nil {
		return err
	}
	if s.repository != nil {
		if err := s.repository.Update(ctx, types.ListenMutation{Policy: &s.current.record, PendingPolicy: &types.ListenPolicyRecord{}}); err != nil {
			return err
		}
	}
	if err := k.Provide(ports.DownloadListenName, s); err != nil {
		return err
	}
	return k.Provide(ports.DownloadListenStatusName, s)
}

func (*Service) Start(context.Context) error { return nil }
func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.serving.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) PrepareConfig(ctx context.Context, view config.View) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var rules []types.DownloadListenRule
	if err := view.Get(rulesField, &rules); err != nil {
		return nil, err
	}
	if len(rules) > 200 {
		return nil, fmt.Errorf("最多支持 200 条监听规则")
	}
	next := &policy{rules: rules, comments: map[types.ChatRef]bool{}, record: types.ListenPolicyRecord{Starts: map[types.ChatRef]int64{}}}
	ids := map[string]bool{}
	for _, rule := range rules {
		if strings.TrimSpace(rule.ID) == "" || ids[rule.ID] {
			return nil, fmt.Errorf("监听规则 ID 为空或重复")
		}
		ids[rule.ID] = true
		if utf8.RuneCountInString(rule.Name) > 100 || len(rule.Sources) == 0 || len(rule.Sources) > 500 {
			return nil, fmt.Errorf("规则 %s 的名称或来源数量无效", rule.ID)
		}
		for _, source := range rule.Sources {
			if !validRef(source) {
				return nil, fmt.Errorf("无效监听来源：%s", source)
			}
			if rule.Enabled {
				next.comments[source] = next.comments[source] || rule.ListenComments
				next.record.Starts[source] = 0
			}
		}
	}
	raw, err := json.Marshal(rules)
	if err != nil {
		return nil, err
	}
	next.record.Signature = fmt.Sprintf("%x", sha256.Sum256(raw))
	s.mu.Lock()
	now := s.clock().Unix()
	previous := s.state.Policy
	if s.state.PendingPolicy.Signature == next.record.Signature {
		previous = s.state.PendingPolicy
	}
	if s.current != nil {
		previous = s.current.record
	}
	for source := range next.record.Starts {
		at := previous.Starts[source]
		if at <= 0 {
			at = now
		}
		next.record.Starts[source] = at
	}
	s.prepared = next
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		s.current = next
		s.state.Policy = next.record
		s.mu.Unlock()
		s.signal()
	}, nil
}

func (s *Service) StageConfig(ctx context.Context, _ config.View) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.repository == nil {
		return nil, fmt.Errorf("监听持久化存储不可用")
	}
	previous := types.ListenPolicyRecord{}
	if s.current != nil {
		previous = s.current.record
	}
	if err := s.repository.Update(ctx, types.ListenMutation{Policy: &previous, PendingPolicy: &s.prepared.record}); err != nil {
		return nil, err
	}
	return func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := s.repository.Update(cleanup, types.ListenMutation{PendingPolicy: &types.ListenPolicyRecord{}}); err != nil {
			s.mu.Lock()
			s.lastError = err.Error()
			s.mu.Unlock()
		}
	}, nil
}

func (s *Service) Reconfigure(ctx context.Context, view config.View) error {
	commit, err := s.PrepareConfig(ctx, view)
	if err != nil {
		return err
	}
	commit()
	return nil
}

func validRef(ref types.ChatRef) bool {
	parts := strings.Split(string(ref), ":")
	if len(parts) != 2 || (parts[0] != kindUser && parts[0] != kindChat && parts[0] != kindChannel) {
		return false
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	return err == nil && id > 0 && strconv.FormatInt(id, 10) == parts[1]
}

func reference(peer types.MessagePeer) types.ChatRef {
	return types.ChatRef(fmt.Sprintf("%s:%d", peer.Kind, peer.ID))
}
func threadID(source types.ChatRef, id int) string { return fmt.Sprintf("%s/%d", source, id) }
func (s *Service) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) persistLocked(ctx context.Context, change types.ListenMutation) error {
	if s.repository == nil {
		return fmt.Errorf("监听持久化存储不可用")
	}
	if err := s.repository.Update(ctx, change); err != nil {
		s.lastError = err.Error()
		return err
	}
	for _, source := range change.Sources {
		s.state.Sources[source.Ref] = source
	}
	for _, thread := range change.Threads {
		s.state.Threads[thread.ID] = thread
	}
	for _, id := range change.DeleteThreads {
		delete(s.state.Threads, id)
	}
	for _, job := range change.Jobs {
		s.state.Jobs[job.ID] = job
	}
	return nil
}

func (s *Service) sourceLocked(ref types.ChatRef) types.ListenSourceRecord {
	source := s.state.Sources[ref]
	at := s.current.record.Starts[ref]
	if source.ActivatedAt != at {
		source = types.ListenSourceRecord{Version: types.ListenRecordVersion, Ref: ref, ActivatedAt: at}
	}
	return source
}

func (s *Service) policySnapshot() *policy {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return nil
	}
	return &policy{comments: maps.Clone(s.current.comments), record: types.ListenPolicyRecord{Starts: maps.Clone(s.current.record.Starts), Signature: s.current.record.Signature}}
}
