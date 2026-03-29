package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/brewexplorer/brew-engine/internal/contract"
	"github.com/brewexplorer/brew-engine/internal/logger"
)

// ── fetchNames ───────────────────────────────────────────────────────────────

func TestFetchNames_Formula_ReturnsSlice(t *testing.T) {
	makeFakeBrew(t, "#!/bin/sh\nprintf 'git\\nwget\\nzsh\\n'\nexit 0\n")

	names, err := fetchNames("--formula")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(names) != 3 {
		t.Errorf("expected 3 names, got %d: %v", len(names), names)
	}
}

func TestFetchNames_SpaceSeparated_ParsedCorrectly(t *testing.T) {
	// Older brew versions may print names space-separated on one line.
	makeFakeBrew(t, "#!/bin/sh\nprintf 'git wget zsh'\nexit 0\n")

	names, err := fetchNames("--formula")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(names) != 3 {
		t.Errorf("strings.Fields must handle space-separated output; got %d names: %v", len(names), names)
	}
}

func TestFetchNames_Empty_ReturnsEmptySlice(t *testing.T) {
	makeFakeBrew(t, "#!/bin/sh\nprintf ''\nexit 0\n")

	names, err := fetchNames("--cask")
	if err != nil {
		t.Fatalf("unexpected error on empty output: %v", err)
	}
	if names == nil || len(names) != 0 {
		t.Errorf("expected empty (non-nil) slice, got %v", names)
	}
}

func TestFetchNames_BrewError_ReturnsError(t *testing.T) {
	makeFakeBrew(t, "#!/bin/sh\nexit 1\n")

	_, err := fetchNames("--formula")
	if err == nil {
		t.Error("expected error when brew exits non-zero, got nil")
	}
}

func TestFetchNames_WhitespaceOnly_ReturnsEmptySlice(t *testing.T) {
	makeFakeBrew(t, "#!/bin/sh\nprintf '   \\n  \\n'\nexit 0\n")

	names, err := fetchNames("--formula")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 0 {
		t.Errorf("whitespace-only output should yield empty slice, got %v", names)
	}
}

// ── runList ──────────────────────────────────────────────────────────────────

func TestRunList_Success_EmitsSingleJSONLine(t *testing.T) {
	makeFakeBrew(t, brewListScript())

	out := captureStdout(t, func() {
		_ = runList(nil, nil)
	})

	responses := decodeResponses(t, out)
	if len(responses) != 1 {
		t.Fatalf("expected 1 response line, got %d: %s", len(responses), out)
	}
	r := responses[0]
	if !r.Success || r.Type != "list" {
		t.Errorf("expected success list response, got success=%v type=%s", r.Success, r.Type)
	}
}

func TestRunList_Success_DataIsNamesList(t *testing.T) {
	makeFakeBrew(t, brewListScript())

	out := captureStdout(t, func() {
		_ = runList(nil, nil)
	})

	responses := decodeResponses(t, out)
	dataBytes, _ := json.Marshal(responses[0].Data)
	var nl contract.NamesList
	if err := json.Unmarshal(dataBytes, &nl); err != nil {
		t.Fatalf("Data is not a NamesList: %v", err)
	}
	if len(nl.Formulae) != 3 {
		t.Errorf("expected 3 formulae, got %d: %v", len(nl.Formulae), nl.Formulae)
	}
	if len(nl.Casks) != 2 {
		t.Errorf("expected 2 casks, got %d: %v", len(nl.Casks), nl.Casks)
	}
	if nl.Total != 5 {
		t.Errorf("expected total=5, got %d", nl.Total)
	}
}

func TestRunList_Success_FormulaeContainExpectedNames(t *testing.T) {
	makeFakeBrew(t, brewListScript())

	out := captureStdout(t, func() {
		_ = runList(nil, nil)
	})

	responses := decodeResponses(t, out)
	dataBytes, _ := json.Marshal(responses[0].Data)
	var nl contract.NamesList
	json.Unmarshal(dataBytes, &nl)

	found := false
	for _, f := range nl.Formulae {
		if f == "wget" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected wget in formulae list, got %v", nl.Formulae)
	}
}

func TestRunList_FormulaError_EmitsErrorJSON(t *testing.T) {
	// brew fails on --formula
	makeFakeBrew(t, "#!/bin/sh\nif [ \"$2\" = \"--formula\" ]; then exit 1; fi\nprintf 'firefox\\n'\nexit 0\n")

	out := captureStdout(t, func() {
		_ = runList(nil, nil)
	})

	responses := decodeResponses(t, out)
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}
	if responses[0].Success || responses[0].Type != "error" {
		t.Errorf("expected error response, got success=%v type=%s", responses[0].Success, responses[0].Type)
	}
	if !strings.Contains(responses[0].Error, "formula") {
		t.Errorf("error message should mention formula, got: %s", responses[0].Error)
	}
}

func TestRunList_CaskError_EmitsErrorJSON(t *testing.T) {
	// brew succeeds for --formula but fails for --cask
	makeFakeBrew(t, "#!/bin/sh\nif [ \"$2\" = \"--formula\" ]; then printf 'git\\n'; exit 0; fi\nexit 1\n")

	out := captureStdout(t, func() {
		_ = runList(nil, nil)
	})

	responses := decodeResponses(t, out)
	if responses[0].Success || responses[0].Type != "error" {
		t.Errorf("expected error response for cask failure, got success=%v type=%s", responses[0].Success, responses[0].Type)
	}
	if !strings.Contains(responses[0].Error, "cask") {
		t.Errorf("error message should mention cask, got: %s", responses[0].Error)
	}
}

func TestRunList_EmptyResults_TotalIsZero(t *testing.T) {
	makeFakeBrew(t, brewListEmptyScript())

	out := captureStdout(t, func() {
		_ = runList(nil, nil)
	})

	responses := decodeResponses(t, out)
	dataBytes, _ := json.Marshal(responses[0].Data)
	var nl contract.NamesList
	json.Unmarshal(dataBytes, &nl)

	if nl.Total != 0 {
		t.Errorf("expected total=0 for empty lists, got %d", nl.Total)
	}
}

func TestRunList_WithLogger_CoversLoggerBranch(t *testing.T) {
	// Initialise the logger so the logger.Sugar != nil branch in runList is hit.
	rawOld, sugarOld := logger.Raw, logger.Sugar
	t.Cleanup(func() { logger.Raw = rawOld; logger.Sugar = sugarOld })

	t.Setenv("BREW_TUI_LOG_DIR", t.TempDir())
	if err := logger.Init(); err != nil {
		t.Fatal(err)
	}

	makeFakeBrew(t, brewListScript())

	out := captureStdout(t, func() {
		_ = runList(nil, nil)
	})

	responses := decodeResponses(t, out)
	if len(responses) != 1 || !responses[0].Success {
		t.Errorf("expected success list response with logger active, got: %s", out)
	}
}

func TestRunList_OutputIsNewlineTerminated(t *testing.T) {
	makeFakeBrew(t, brewListScript())

	out := captureStdout(t, func() {
		_ = runList(nil, nil)
	})

	if !strings.HasSuffix(out, "\n") {
		t.Error("output must be newline-terminated")
	}
}
