package cmd

import (
	"testing"

	"github.com/brewexplorer/brew-engine/internal/cache"
)

// TestRunWatch_NoValidDirs_EmitsErrorJSON verifies that runWatch emits a
// Type="error" JSON response and returns nil when no Homebrew watch directories
// are available (e.g. on a machine without Homebrew installed).
// This tests the only deterministic branch of runWatch that exits immediately.
func TestRunWatch_NoValidDirs_EmitsErrorJSON(t *testing.T) {
	orig := cache.BrewWatchDirs
	t.Cleanup(func() { cache.BrewWatchDirs = orig })
	cache.BrewWatchDirs = []string{"/nonexistent/path/that/does/not/exist"}

	out := captureStdout(t, func() {
		_ = runWatch(nil, nil)
	})

	responses := decodeResponses(t, out)
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d: %s", len(responses), out)
	}
	if responses[0].Success || responses[0].Type != "error" {
		t.Errorf("expected error response, got success=%v type=%s", responses[0].Success, responses[0].Type)
	}
	if responses[0].Error == "" {
		t.Error("error field must not be empty")
	}
}
