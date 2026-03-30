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

// ── runRefresh — basic behavior ───────────────────────────────────────────────

func TestRunRefresh_EmitsSingleJSONLine(t *testing.T) {
	t.Setenv("BREW_TUI_CACHE_DIR", t.TempDir())
	makeFakeBrew(t, brewListScript())

	out := captureStdout(t, func() { _ = runRefresh(nil, nil) })

	responses := decodeResponses(t, out)
	if len(responses) != 1 {
		t.Fatalf("expected 1 response line, got %d: %s", len(responses), out)
	}
	r := responses[0]
	if !r.Success || r.Type != "list" {
		t.Errorf("expected success list response, got success=%v type=%s", r.Success, r.Type)
	}
}

func TestRunRefresh_DataIsNamesList(t *testing.T) {
	t.Setenv("BREW_TUI_CACHE_DIR", t.TempDir())
	makeFakeBrew(t, brewListScript())

	out := captureStdout(t, func() { _ = runRefresh(nil, nil) })

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

func TestRunRefresh_WritesListJsonFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BREW_TUI_CACHE_DIR", dir)
	makeFakeBrew(t, brewListScript())

	captureStdout(t, func() { _ = runRefresh(nil, nil) })

	if _, err := os.Stat(filepath.Join(dir, "list.json")); os.IsNotExist(err) {
		t.Error("expected list.json to be created after refresh")
	}
}

func TestRunRefresh_OutputIsNewlineTerminated(t *testing.T) {
	t.Setenv("BREW_TUI_CACHE_DIR", t.TempDir())
	makeFakeBrew(t, brewListScript())

	out := captureStdout(t, func() { _ = runRefresh(nil, nil) })

	if !strings.HasSuffix(out, "\n") {
		t.Error("output must be newline-terminated")
	}
}

// ── runRefresh — invalidates existing cache ───────────────────────────────────

func TestRunRefresh_InvalidatesExistingCache(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BREW_TUI_CACHE_DIR", dir)

	// Pre-seed cache with stale data
	stalePayload := `{"success":true,"type":"list","data":{"formulae":["stale-pkg"],"casks":[],"total":1}}` + "\n"
	if err := cache.WriteList([]byte(stalePayload)); err != nil {
		t.Fatal(err)
	}

	// Refresh should replace with fresh data
	makeFakeBrew(t, brewListScript())
	out := captureStdout(t, func() { _ = runRefresh(nil, nil) })

	// Should NOT contain stale data
	if strings.Contains(out, "stale-pkg") {
		t.Error("refresh should have replaced stale cache, but stale-pkg still present")
	}

	// Should contain fresh data from fake brew
	responses := decodeResponses(t, out)
	dataBytes, _ := json.Marshal(responses[0].Data)
	var nl contract.NamesList
	json.Unmarshal(dataBytes, &nl)

	if len(nl.Formulae) != 3 {
		t.Errorf("expected 3 formulae from fresh fetch, got %d", len(nl.Formulae))
	}
}

func TestRunRefresh_AlwaysInvokesBrew(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BREW_TUI_CACHE_DIR", dir)

	// Pre-seed cache
	payload := `{"success":true,"type":"list","data":{"formulae":["cached"],"casks":[],"total":1}}` + "\n"
	if err := cache.WriteList([]byte(payload)); err != nil {
		t.Fatal(err)
	}

	// Unlike list (which serves from cache), refresh always calls brew
	makeFakeBrew(t, brewListScript())
	out := captureStdout(t, func() { _ = runRefresh(nil, nil) })

	// Result should be from fake brew, not cache
	if strings.Contains(out, "cached") {
		t.Error("refresh should invoke brew, not serve from cache")
	}
	if !strings.Contains(out, "wget") {
		t.Error("refresh should contain wget from fake brew output")
	}
}

// ── runRefresh — error handling ───────────────────────────────────────────────

func TestRunRefresh_FormulaError_EmitsErrorJSON(t *testing.T) {
	t.Setenv("BREW_TUI_CACHE_DIR", t.TempDir())
	makeFakeBrew(t, "#!/bin/sh\nif [ \"$2\" = \"--formula\" ]; then exit 1; fi\nprintf 'firefox\\n'\nexit 0\n")

	out := captureStdout(t, func() { _ = runRefresh(nil, nil) })

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

func TestRunRefresh_CaskError_EmitsErrorJSON(t *testing.T) {
	t.Setenv("BREW_TUI_CACHE_DIR", t.TempDir())
	makeFakeBrew(t, "#!/bin/sh\nif [ \"$2\" = \"--formula\" ]; then printf 'git\\n'; exit 0; fi\nexit 1\n")

	out := captureStdout(t, func() { _ = runRefresh(nil, nil) })

	responses := decodeResponses(t, out)
	if responses[0].Success || responses[0].Type != "error" {
		t.Errorf("expected error response for cask failure, got success=%v type=%s", responses[0].Success, responses[0].Type)
	}
	if !strings.Contains(responses[0].Error, "cask") {
		t.Errorf("error message should mention cask, got: %s", responses[0].Error)
	}
}

// ── runRefresh — with logger ──────────────────────────────────────────────────

func TestRunRefresh_WithLogger_CoversLoggerBranches(t *testing.T) {
	t.Setenv("BREW_TUI_CACHE_DIR", t.TempDir())
	rawOld, sugarOld := logger.Raw, logger.Sugar
	t.Cleanup(func() { logger.Raw = rawOld; logger.Sugar = sugarOld })

	t.Setenv("BREW_TUI_LOG_DIR", t.TempDir())
	if err := logger.Init(); err != nil {
		t.Fatal(err)
	}

	makeFakeBrew(t, brewListScript())

	out := captureStdout(t, func() { _ = runRefresh(nil, nil) })

	responses := decodeResponses(t, out)
	if len(responses) != 1 || !responses[0].Success {
		t.Errorf("expected success list response with logger active, got: %s", out)
	}
}

// ── runRefresh — no cache file exists ─────────────────────────────────────────

func TestRunRefresh_NoCacheFile_StillSucceeds(t *testing.T) {
	// Empty temp dir — no list.json exists
	t.Setenv("BREW_TUI_CACHE_DIR", t.TempDir())
	makeFakeBrew(t, brewListScript())

	out := captureStdout(t, func() { _ = runRefresh(nil, nil) })

	responses := decodeResponses(t, out)
	if len(responses) != 1 || !responses[0].Success {
		t.Errorf("refresh should succeed even with no existing cache, got: %s", out)
	}
}
