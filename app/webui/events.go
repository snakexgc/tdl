package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/snakexgc/tdl/bsw/services/telemetry"
	"github.com/snakexgc/tdl/pkg/config"
)

const statusTopic = "status"

// HTTP owns commands and snapshots. This authenticated same-origin socket
// carries only observations; slow readers cannot block workers or other peers.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	if s.opts.Context != nil {
		stop := context.AfterFunc(s.opts.Context, cancel)
		defer stop()
	}
	conn.SetReadLimit(4096)
	var subscribed atomic.Pointer[[]string]
	wake := make(chan struct{}, 1)
	readerDone := make(chan struct{})
	defer func() {
		cancel()
		conn.CloseNow()
		<-readerDone
	}()
	go func() {
		defer close(readerDone)
		defer cancel()
		for {
			var request struct {
				Topics []string `json:"topics"`
			}
			if wsjson.Read(ctx, conn, &request) != nil {
				return
			}
			topics := []string{statusTopic}
			seen := map[string]bool{statusTopic: true}
			for _, topic := range request.Topics {
				if !seen[topic] && s.snapshotSource(topic) != nil {
					topics = append(topics, topic)
					seen[topic] = true
				}
			}
			subscribed.Store(&topics)
			select {
			case wake <- struct{}{}:
			default:
			}
		}
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	last := map[string]string{}
	lastPing := time.Now()
	for {
		if !s.sessionOK(r) {
			_ = conn.Close(websocket.StatusPolicyViolation, "session expired")
			return
		}
		topics := []string{statusTopic}
		if current := subscribed.Load(); current != nil {
			topics = *current
		}
		for _, topic := range topics {
			bounded, done := context.WithTimeout(ctx, 2*time.Second)
			data, sampleErr := s.samples.Read(bounded, topic, s.snapshotSource(topic))
			done()
			packet := struct {
				Topic string          `json:"topic"`
				Data  json.RawMessage `json:"data,omitempty"`
				Error string          `json:"error,omitempty"`
			}{Topic: topic, Data: data}
			if sampleErr != nil {
				packet.Data = nil
				packet.Error = sampleErr.Error()
			}
			encoded, marshalErr := json.Marshal(packet)
			if marshalErr != nil {
				return
			}
			if last[topic] == string(encoded) {
				continue
			}
			bounded, done = context.WithTimeout(ctx, 3*time.Second)
			err = conn.Write(bounded, websocket.MessageText, encoded)
			done()
			if err != nil {
				return
			}
			last[topic] = string(encoded)
		}
		if time.Since(lastPing) >= 20*time.Second {
			bounded, done := context.WithTimeout(ctx, 5*time.Second)
			err = conn.Ping(bounded)
			done()
			if err != nil {
				return
			}
			lastPing = time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-wake:
			last = map[string]string{} // resubscription always gets a full snapshot
		}
	}
}

func (s *Server) snapshotSource(topic string) telemetry.Source {
	switch topic {
	case statusTopic:
		return func(context.Context) (any, error) { return s.statusSnapshot(), nil }
	case "dashboard":
		return s.dashboardSnapshot
	case "downloads":
		return s.internalDownloadsSnapshot
	case "download-tasks-local":
		return s.downloadTasksSnapshot(localDownloadExecutor)
	case "download-tasks-aria2":
		return s.downloadTasksSnapshot(config.DownloaderModeAria2)
	case "forwards":
		return s.forwardsSnapshot
	default:
		return nil
	}
}
