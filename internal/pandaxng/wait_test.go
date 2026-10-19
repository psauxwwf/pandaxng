package pandaxng

import (
	"strings"
	"testing"
)

func TestNormalizeWaitUntil(t *testing.T) {
	for _, mode := range []string{"", "load", "domcontentloaded", "networkalmostidle", "networkidle", "done"} {
		want := mode
		if want == "" {
			want = "done"
		}
		if got, err := NormalizeWaitUntil(mode, DefaultFetchWaitUntil); err != nil || got != want {
			t.Fatalf("mode %q: got %q, %v; want %q", mode, got, err, want)
		}
	}
	for _, mode := range []string{"none", "invalid", "LOAD", " done "} {
		if _, err := NormalizeWaitUntil(mode, DefaultFetchWaitUntil); err == nil || !strings.Contains(err.Error(), "wait-until must") {
			t.Fatalf("mode %q: expected validation error, got %v", mode, err)
		}
	}
	if got, err := NormalizeWaitUntil("", DefaultSearchWaitUntil); err != nil || got != "domcontentloaded" {
		t.Fatalf("unexpected search default: %q, %v", got, err)
	}
}
