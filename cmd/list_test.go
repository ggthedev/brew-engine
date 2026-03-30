package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brewexplorer/brew-engine/internal/cache"
	"github.com/brewexplorer/brew-engine/internal/contract"
	"github.com/brewexplorer/brew-engine/internal/logger"
)

// ── runList — cache miss (brew invoked) ──────────────────────────────────────

func TestRunList_CacheMiss_EmitsSingleJSONLine(t *testing.T) {
	t.Setenv("BREW_TUI_CACHE_DIR", t.TempDir())
	makeFakeBrew(t, brewListScript())

	out := captureStdout(t, func() { _ = runList(nil, nil) })

	responses := decodeResponses(t, out)
	if len(responses) != 1 {
		t.Fatalf("expected 1 response line, got %d: %s", len(responses), out)
	}
	r := responses[0]
	if !r.Success || r.Type != "list" {
		t.Errorf("expected success list response, got success=%v type=%s", r.Success, r.Type)
	}
}

func TestRunList_CacheMiss_DataIsNamesList(t *testing.T) {
	t.Setenv("BREW_TUI_CACHE_DIR", t.TempDir())
	makeFakeBrew(t, brewListScript())

	out := captureStdout(t, func() { _ = runList(nil, nil) })

	responses := decodeResponses(t, out)
	dataBytes, _ := json.Marshal(responses[0].Data)
	var nl contract.NamesList
	if err := json.Unmarshal(dataBytes, &nl); err != nil {
		t.Fatalf("Data is not a NamesList: %v", err)
	}
	if len(nl.Formulae) != 3 || len(nl.Casks) != 2 || nl.Total != 5 {
		t.Errorf("unexpected NamesList: %+v", nl)
	}
}

func TestRunList_CacheMiss_FormulaeContainExpectedNames(t *testing.T) {
	t.Setenv("BREW_TUI_CACHE_DIR", t.TempDir())
	makeFakeBrew(t, brewListScript())

	out := captureStdout(t, func() { _ = runList(nil, nil) })

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

func TestRunList_CacheMiss_WritesListJsonFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BREW_TUI_CACHE_DIR", dir)
	makeFakeBrew(t, brewListScript())

	captureStdout(t, func() { _ = runList(nil, nil) })

	if _, err := os.Stat(filepath.Join(dir, "list.json")); os.IsNotExist(err) {
		t.Error("expected list.json to be created after cache miss")
	}
}

func TestRunList_FormulaError_EmitsErrorJSON(t *testing.T) {
	t.Setenv("BREW_TUI_CACHE_DIR", t.TempDir())
	makeFakeBrew(t, "#!/bin/sh\nif [ \"$2\" = \"--formula\" ]; then exit 1; fi\nprintf 'firefox\\n'\nexit 0\n")

	out := captureStdout(t, func() { _ = runList(nil, nil) })

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
	t.Setenv("BREW_TUI_CACHE_DIR", t.TempDir())
	makeFakeBrew(t, "#!/bin/sh\nif [ \"$2\" = \"--formula\" ]; then printf 'git\\n'; exit 0; fi\nexit 1\n")

	out := captureStdout(t, func() { _ = runList(nil, nil) })

	responses := decodeResponses(t, out)
	if responses[0].Success || responses[0].Type != "error" {
		t.Errorf("expected error response for cask failure, got success=%v type=%s", responses[0].Success, responses[0].Type)
	}
	if !strings.Contains(responses[0].Error, "cask") {
		t.Errorf("error message should mention cask, got: %s", responses[0].Error)
	}
}

func TestRunList_EmptyResults_TotalIsZero(t *testing.T) {
	t.Setenv("BREW_TUI_CACHE_DIR", t.TempDir())
	makeFakeBrew(t, brewListEmptyScript())

	out := captureStdout(t, func() { _ = runList(nil, nil) })

	responses := decodeResponses(t, out)
	dataBytes, _ := json.Marshal(responses[0].Data)
	var nl contract.NamesList
	json.Unmarshal(dataBytes, &nl)

	if nl.Total != 0 {
		t.Errorf("expected total=0 for empty lists, got %d", nl.Total)
	}
}

// ── runList — cache hit ───────────────────────────────────────────────────────

func TestRunList_CacheHit_ServesFromFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BREW_TUI_CACHE_DIR", dir)

	// Pre-seed the cache with known content.
	payload := `{"success":true,"type":"list","data":{"formulae":["cached-pkg"],"casks":[],"total":1}}` + "\n"
	if err := cache.WriteList([]byte(payload)); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() { _ = runList(nil, nil) })

	if out != payload {
		t.Errorf("expected raw cache bytes on stdout, got:\n%s", out)
	}
}

func TestRunList_CacheHit_BrewIsNotInvoked(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BREW_TUI_CACHE_DIR", dir)

	payload := `{"success":true,"type":"list","data":{"formulae":[],"casks":[],"total":0}}` + "\n"
	if err := cache.WriteList([]byte(payload)); err != nil {
		t.Fatal(err)
	}

	// No fake brew in PATH — if brew were invoked the test would fail or return an error.
	t.Setenv("PATH", t.TempDir())

	out := captureStdout(t, func() { _ = runList(nil, nil) })

	responses := decodeResponses(t, out)
	if len(responses) != 1 || !responses[0].Success {
		t.Errorf("cache hit should succeed without brew, got: %s", out)
	}
}

// ── runList — with logger ────────────────────────────────────────────────────

func TestRunList_WithLogger_CacheMiss_CoversLoggerBranch(t *testing.T) {
	t.Setenv("BREW_TUI_CACHE_DIR", t.TempDir())
	rawOld, sugarOld := logger.Raw, logger.Sugar
	t.Cleanup(func() { logger.Raw = rawOld; logger.Sugar = sugarOld })

	t.Setenv("BREW_TUI_LOG_DIR", t.TempDir())
	if err := logger.Init(); err != nil {
		t.Fatal(err)
	}

	makeFakeBrew(t, brewListScript())

	out := captureStdout(t, func() { _ = runList(nil, nil) })

	responses := decodeResponses(t, out)
	if len(responses) != 1 || !responses[0].Success {
		t.Errorf("expected success list response with logger active, got: %s", out)
	}
}

func TestRunList_WithLogger_CacheHit_CoversLoggerBranch(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BREW_TUI_CACHE_DIR", dir)
	rawOld, sugarOld := logger.Raw, logger.Sugar
	t.Cleanup(func() { logger.Raw = rawOld; logger.Sugar = sugarOld })

	t.Setenv("BREW_TUI_LOG_DIR", t.TempDir())
	if err := logger.Init(); err != nil {
		t.Fatal(err)
	}

	payload := `{"success":true,"type":"list","data":{"formulae":[],"casks":[],"total":0}}` + "\n"
	if err := cache.WriteList([]byte(payload)); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() { _ = runList(nil, nil) })

	if out != payload {
		t.Errorf("expected cached payload on stdout, got: %s", out)
	}
}

func TestRunList_OutputIsNewlineTerminated(t *testing.T) {
	t.Setenv("BREW_TUI_CACHE_DIR", t.TempDir())
	makeFakeBrew(t, brewListScript())

	out := captureStdout(t, func() { _ = runList(nil, nil) })

	if !strings.HasSuffix(out, "\n") {
		t.Error("output must be newline-terminated")
	}
}
