package taskhub

import (
	"encoding/json"
	"errors"
	"time"
)

// LinkActivity reads the required sliding expiry clock of a current link record.
func LinkActivity(data []byte) (time.Time, error) {
	var record struct {
		LastActiveAt time.Time `json:"last_active_at"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return time.Time{}, err
	}
	if record.LastActiveAt.IsZero() {
		return time.Time{}, errors.New("download link last_active_at is required")
	}
	return record.LastActiveAt, nil
}
