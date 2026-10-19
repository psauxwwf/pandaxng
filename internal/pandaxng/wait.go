package pandaxng

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	DefaultSearchWaitUntil = "domcontentloaded"
	DefaultFetchWaitUntil  = "done"
	DefaultSearchTimeout   = 20 * time.Second
	DefaultFetchTimeout    = 40 * time.Second
)

func WaitUntilModes() []string {
	return []string{"load", "domcontentloaded", "networkalmostidle", "networkidle", "done"}
}

// NormalizeWaitUntil preserves the default for callers that omit the option.
func NormalizeWaitUntil(mode, fallback string) (string, error) {
	if mode == "" {
		mode = fallback
	}
	if !slices.Contains(WaitUntilModes(), mode) {
		return "", fmt.Errorf("wait-until must be one of: %s", strings.Join(WaitUntilModes(), ", "))
	}
	return mode, nil
}
