package accounttelegram

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/snakexgc/tdl/interfaces/ports"
)

type Sessions struct {
	current    string
	repository ports.SessionRepository
}

func NewSessions(current string, repository ports.SessionRepository) *Sessions {
	return &Sessions{current, repository}
}

func validSessionName(name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

func (s *Sessions) List(ctx context.Context) ([]ports.SessionOption, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	names, err := s.repository.List(ctx)
	if err != nil {
		return nil, err
	}
	result := []ports.SessionOption{}
	seen := map[string]bool{}
	for _, name := range names {
		if !validSessionName(name) || seen[name] {
			continue
		}
		seen[name] = true
		result = append(result, ports.SessionOption{Namespace: name, Current: name == s.current})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Current != result[j].Current {
			return result[i].Current
		}
		return result[i].Namespace < result[j].Namespace
	})
	return result, nil
}

func (s *Sessions) Delete(ctx context.Context, name string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if !validSessionName(name) {
		return 0, errors.New("invalid session name")
	}
	if name == s.current {
		return 0, errors.New("当前用户正在运行中，请先切换到其他用户后再删除。")
	}
	return s.repository.Delete(ctx, name)
}
