package httpdl

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/go-faster/errors"
)

const httpDeliveryField = "http_delivery"

type persistentHTTPDeliveryRange struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

type persistentHTTPDelivery struct {
	FileSize    int64                         `json:"file_size"`
	Ranges      []persistentHTTPDeliveryRange `json:"ranges,omitempty"`
	CompletedAt time.Time                     `json:"completed_at,omitempty"`
}

// DownloadTaskHTTPStatus describes bytes that the HTTP server has successfully
// sent for a persistent download link. DeliveredBytes counts the union of
// completed response ranges, so retries and overlapping ranges are not counted
// twice. Completed is true only after that union covers the complete file.
type DownloadTaskHTTPStatus struct {
	Completed      bool
	CompletedAt    time.Time
	DeliveredBytes int64
	FileSize       int64
}

// ParseDownloadTaskHTTPStatus reads the HTTP delivery state embedded in a
// persistent download-task JSON record.
func ParseDownloadTaskHTTPStatus(data []byte) (DownloadTaskHTTPStatus, error) {
	var envelope struct {
		HTTPDelivery persistentHTTPDelivery `json:"http_delivery"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return DownloadTaskHTTPStatus{}, errors.Wrap(err, "decode HTTP delivery status")
	}

	delivery := envelope.HTTPDelivery
	status := DownloadTaskHTTPStatus{
		Completed:   !delivery.CompletedAt.IsZero(),
		CompletedAt: delivery.CompletedAt,
		FileSize:    delivery.FileSize,
	}
	if status.Completed {
		status.DeliveredBytes = delivery.FileSize
		return status, nil
	}
	for _, delivered := range mergeHTTPDeliveryRanges(delivery.Ranges, delivery.FileSize) {
		status.DeliveredBytes += delivered.End - delivered.Start + 1
	}
	return status, nil
}

// recordHTTPDelivery adds successfully-sent byte ranges to the task's
// persistent delivery state. It is serialized with other task-store writes so
// concurrent external-downloader ranges cannot overwrite one another.
func (s *taskStore) recordHTTPDelivery(ctx context.Context, id string, fileSize int64, ranges []downloadRange, completedAt time.Time) (bool, error) {
	if s == nil || s.kv == nil {
		return false, nil
	}
	if fileSize < 0 {
		return false, errors.New("invalid HTTP delivery file size")
	}
	if completedAt.IsZero() {
		completedAt = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := downloadTaskStorageKey(id)
	data, err := s.kv.Get(ctx, key)
	if err != nil {
		return false, errors.Wrap(err, "load HTTP delivery task")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return false, errors.Wrap(err, "decode HTTP delivery task")
	}
	if raw == nil {
		raw = map[string]json.RawMessage{}
	}

	var delivery persistentHTTPDelivery
	if value := raw[httpDeliveryField]; len(value) > 0 {
		if err := json.Unmarshal(value, &delivery); err != nil {
			return false, errors.Wrap(err, "decode persisted HTTP delivery")
		}
	}
	if delivery.FileSize != fileSize && (len(delivery.Ranges) > 0 || !delivery.CompletedAt.IsZero()) {
		delivery = persistentHTTPDelivery{}
	}
	delivery.FileSize = fileSize
	if !delivery.CompletedAt.IsZero() {
		return false, nil
	}

	added := make([]persistentHTTPDeliveryRange, 0, len(ranges))
	for _, selected := range ranges {
		added = append(added, persistentHTTPDeliveryRange{Start: selected.start, End: selected.end})
	}
	delivery.Ranges = mergeHTTPDeliveryRanges(append(delivery.Ranges, added...), fileSize)
	complete := fileSize == 0 || (len(delivery.Ranges) == 1 && delivery.Ranges[0].Start == 0 && delivery.Ranges[0].End == fileSize-1)
	if complete {
		delivery.CompletedAt = completedAt
		delivery.Ranges = nil
		raw["downloaded"] = json.RawMessage("true")
	}

	deliveryData, err := json.Marshal(delivery)
	if err != nil {
		return false, errors.Wrap(err, "encode HTTP delivery")
	}
	raw[httpDeliveryField] = deliveryData
	updated, err := json.Marshal(raw)
	if err != nil {
		return false, errors.Wrap(err, "encode HTTP delivery task")
	}
	if err := s.kv.Set(ctx, key, updated); err != nil {
		return false, errors.Wrap(err, "persist HTTP delivery task")
	}
	return complete, nil
}

func mergeHTTPDeliveryRanges(ranges []persistentHTTPDeliveryRange, fileSize int64) []persistentHTTPDeliveryRange {
	if fileSize <= 0 || len(ranges) == 0 {
		return nil
	}

	normalized := make([]persistentHTTPDeliveryRange, 0, len(ranges))
	for _, delivered := range ranges {
		if delivered.Start < 0 {
			delivered.Start = 0
		}
		if delivered.End >= fileSize {
			delivered.End = fileSize - 1
		}
		if delivered.End < delivered.Start || delivered.Start >= fileSize {
			continue
		}
		normalized = append(normalized, delivered)
	}
	if len(normalized) == 0 {
		return nil
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].Start == normalized[j].Start {
			return normalized[i].End < normalized[j].End
		}
		return normalized[i].Start < normalized[j].Start
	})

	merged := normalized[:1]
	for _, next := range normalized[1:] {
		current := &merged[len(merged)-1]
		if next.Start <= current.End || next.Start == current.End+1 {
			if next.End > current.End {
				current.End = next.End
			}
			continue
		}
		merged = append(merged, next)
	}
	return merged
}

// mergeDownloadTaskData overlays current task metadata while preserving
// status fields owned by HTTP, aria2/internal synchronization, and future
// versions of the record.
func mergeDownloadTaskData(existing, current []byte) ([]byte, error) {
	var oldRaw map[string]json.RawMessage
	if err := json.Unmarshal(existing, &oldRaw); err != nil {
		return nil, err
	}
	var currentRaw map[string]json.RawMessage
	if err := json.Unmarshal(current, &currentRaw); err != nil {
		return nil, err
	}
	if oldRaw == nil {
		oldRaw = map[string]json.RawMessage{}
	}
	for key, value := range currentRaw {
		oldRaw[key] = value
	}
	return json.Marshal(oldRaw)
}
