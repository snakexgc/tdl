package storage

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/gotd/td/telegram/updates"

	"github.com/snakexgc/tdl/internal/core/storage/keygen"
)

type State struct {
	kv Storage
}

func NewState(kv Storage) updates.StateStorage {
	return &State{kv: kv}
}

func (s *State) Get(ctx context.Context, key string, v interface{}) error {
	data, err := s.kv.Get(ctx, key)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, v)
}

func (s *State) Set(ctx context.Context, key string, v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}

	return s.kv.Set(ctx, key, data)
}

func (s *State) GetState(ctx context.Context, userID int64) (updates.State, bool, error) {
	state := updates.State{}

	if err := s.Get(ctx, s.stateKey(userID), &state); err != nil {
		if errors.Is(err, ErrNotFound) {
			return state, false, nil
		}
		return state, false, err
	}

	return state, true, nil
}

func (s *State) SetState(ctx context.Context, userID int64, state updates.State) error {
	return Update(ctx, s.kv, func(tx Storage) error {
		scoped := &State{kv: tx}
		if err := scoped.Set(ctx, s.stateKey(userID), state); err != nil {
			return err
		}
		if _, err := tx.Get(ctx, s.channelKey(userID)); errors.Is(err, ErrNotFound) {
			return scoped.Set(ctx, s.channelKey(userID), map[int64]int{})
		} else {
			return err
		}
	})
}

func (s *State) mutate(ctx context.Context, userID int64, fn func(*updates.State)) error {
	return Update(ctx, s.kv, func(tx Storage) error {
		scoped := &State{kv: tx}
		var state updates.State
		if err := scoped.Get(ctx, s.stateKey(userID), &state); err != nil {
			return err
		}
		fn(&state)
		return scoped.Set(ctx, s.stateKey(userID), state)
	})
}

func (s *State) SetPts(ctx context.Context, id int64, v int) error {
	return s.mutate(ctx, id, func(state *updates.State) { state.Pts = v })
}

func (s *State) SetQts(ctx context.Context, id int64, v int) error {
	return s.mutate(ctx, id, func(state *updates.State) { state.Qts = v })
}

func (s *State) SetDate(ctx context.Context, id int64, v int) error {
	return s.mutate(ctx, id, func(state *updates.State) { state.Date = v })
}

func (s *State) SetSeq(ctx context.Context, id int64, v int) error {
	return s.mutate(ctx, id, func(state *updates.State) { state.Seq = v })
}

func (s *State) SetDateSeq(ctx context.Context, id int64, date, seq int) error {
	return s.mutate(ctx, id, func(state *updates.State) { state.Date = date; state.Seq = seq })
}

func (s *State) GetChannelPts(ctx context.Context, userID, channelID int64) (int, bool, error) {
	c := make(map[int64]int)

	if err := s.Get(ctx, s.channelKey(userID), &c); err != nil {
		if errors.Is(err, ErrNotFound) {
			return 0, false, nil
		}
		return 0, false, err
	}

	pts, ok := c[channelID]
	if !ok {
		return 0, false, nil
	}

	return pts, true, nil
}

func (s *State) SetChannelPts(ctx context.Context, userID, channelID int64, pts int) error {
	return Update(ctx, s.kv, func(tx Storage) error {
		scoped := &State{kv: tx}
		c := make(map[int64]int)
		key := s.channelKey(userID)
		if err := scoped.Get(ctx, key, &c); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if c == nil {
			c = make(map[int64]int)
		}
		c[channelID] = pts
		return scoped.Set(ctx, key, c)
	})
}

func (s *State) ForEachChannels(ctx context.Context, userID int64, f func(ctx context.Context, channelID int64, pts int) error) error {
	c := make(map[int64]int)

	if err := s.Get(ctx, s.channelKey(userID), &c); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}

	for channelID, pts := range c {
		if err := f(ctx, channelID, pts); err != nil {
			return err
		}
	}

	return nil
}

func (s *State) stateKey(userID int64) string {
	return keygen.New("state", strconv.FormatInt(userID, 10))
}

func (s *State) channelKey(userID int64) string {
	return keygen.New("chan", strconv.FormatInt(userID, 10))
}
