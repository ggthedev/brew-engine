package cmd

import (
	"testing"
)

// ── runRemove ────────────────────────────────────────────────────────────────

func TestRunRemove_Success_EmitsDoneEvent(t *testing.T) {
	makeFakeBrew(t, "#!/bin/sh\nprintf '==> Uninstalling wget\\n'\nexit 0\n")

	out := captureStdout(t, func() {
		_ = runRemove(nil, []string{"wget"})
	})

	events := decodeResponses(t, out)
	if len(events) == 0 {
		t.Fatalf("expected at least one event, got none")
	}
	last := events[len(events)-1]
	if last.Type != "done" || !last.Success {
		t.Errorf("expected final done/success event, got type=%s success=%v", last.Type, last.Success)
	}
}

func TestRunRemove_Success_EmitsProgressEvent(t *testing.T) {
	makeFakeBrew(t, "#!/bin/sh\nprintf '==> Uninstalling wget\\n'\nexit 0\n")

	out := captureStdout(t, func() {
		_ = runRemove(nil, []string{"wget"})
	})

	events := decodeResponses(t, out)
	progressCount := 0
	for _, e := range events {
		if e.Type == "progress" {
			progressCount++
		}
	}
	if progressCount != 1 {
		t.Errorf("expected 1 progress event, got %d", progressCount)
	}
}

func TestRunRemove_BrewNotFound_EmitsErrorEvent(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	out := captureStdout(t, func() {
		_ = runRemove(nil, []string{"wget"})
	})

	events := decodeResponses(t, out)
	if len(events) != 1 || events[0].Type != "error" {
		t.Errorf("expected single error event when brew not found, got %d events", len(events))
	}
}

func TestRunRemove_NonZeroExit_EmitsErrorNotDone(t *testing.T) {
	makeFakeBrew(t, "#!/bin/sh\nexit 1\n")

	out := captureStdout(t, func() {
		_ = runRemove(nil, []string{"wget"})
	})

	events := decodeResponses(t, out)
	last := events[len(events)-1]
	if last.Type != "error" {
		t.Errorf("expected final error event on non-zero exit, got type=%s", last.Type)
	}
}

func TestRunRemove_AlwaysReturnsNilToCobraFromRunE(t *testing.T) {
	makeFakeBrew(t, "#!/bin/sh\nexit 1\n")

	captureStdout(t, func() {
		err := runRemove(nil, []string{"wget"})
		if err != nil {
			t.Errorf("RunE handler must always return nil, got %v", err)
		}
	})
}
