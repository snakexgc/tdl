package httpdl

import (
	"fmt"
	"strings"

	"github.com/go-faster/errors"

	"github.com/iyear/tdl/pkg/config"
)

func validateHTTPBufferConfig(cfg config.HTTPBufferConfig) error {
	switch normalizeHTTPBufferMode(cfg.Mode) {
	case httpBufferModeOff, httpBufferModeMemory:
	default:
		return fmt.Errorf("http.buffer.mode must be %q or %q", httpBufferModeOff, httpBufferModeMemory)
	}
	if cfg.SizeMB < 0 {
		return errors.New("http.buffer.size_mb must be greater than or equal to 0")
	}
	return nil
}

func ValidateBufferConfig(cfg config.HTTPBufferConfig) error {
	return validateHTTPBufferConfig(cfg)
}

func normalizeHTTPBufferMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return httpBufferModeMemory
	}
	return mode
}

func NormalizeBufferMode(mode string) string {
	return normalizeHTTPBufferMode(mode)
}

func normalizedHTTPBufferSizeMB(cfg config.HTTPBufferConfig) int {
	if cfg.SizeMB < 1 {
		return 1
	}
	return cfg.SizeMB
}

func NormalizedBufferSizeMB(cfg config.HTTPBufferConfig) int {
	return normalizedHTTPBufferSizeMB(cfg)
}

func httpMemoryBufferSlots(cfg config.HTTPBufferConfig) int {
	if normalizeHTTPBufferMode(cfg.Mode) != httpBufferModeMemory {
		return 0
	}

	sizeBytes := int64(normalizedHTTPBufferSizeMB(cfg)) * 1024 * 1024
	slots := int((sizeBytes + int64(downloadStreamPartSize) - 1) / int64(downloadStreamPartSize))
	if slots < 1 {
		return 1
	}
	return slots
}

func MemoryBufferSlots(cfg config.HTTPBufferConfig) int {
	return httpMemoryBufferSlots(cfg)
}
