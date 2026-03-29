package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brewexplorer/brew-engine/internal/cache"
	"github.com/brewexplorer/brew-engine/internal/contract"
	"github.com/brewexplorer/brew-engine/internal/logger"
)

// ── runInfo ──────────────────────────────────────────────────────────────────

func TestRunInfo_Formula_Installed(t *testing.T) {
	t.Setenv("BREW_TUI_CACHE_DIR", t.TempDir())
	makeFakeBrew(t, "#!/bin/sh\nprintf '"+formulaInfoJSON+"'\nexit 0\n")

	out := captureStdout(t, func() {
		_ = runInfo(nil, []string{"wget"})
	})

	responses := decodeResponses(t, out)
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}
	r := responses[0]
	if !r.Success || r.Type != "info" {
		t.Errorf("expected success info response, got success=%v type=%s", r.Success, r.Type)
	}

	dataBytes, _ := json.Marshal(r.Data)
	var fi contract.FormulaInfo
	if err := json.Unmarshal(dataBytes, &fi); err != nil {
		t.Fatalf("Data is not FormulaInfo: %v", err)
	}
	if fi.Name != "wget" {
		t.Errorf("expected name=wget, got %s", fi.Name)
	}
	if !fi.Installed {
		t.Error("formula should be marked as installed")
	}
	if fi.InstalledVersion != "1.25.0" {
		t.Errorf("expected installed_version=1.25.0, got %s", fi.InstalledVersion)
	}
}

func TestRunInfo_Formula_NotInstalled(t *testing.T) {
	t.Setenv("BREW_TUI_CACHE_DIR", t.TempDir())
	makeFakeBrew(t, "#!/bin/sh\nprintf '"+formulaInfoNotInstalledJSON+"'\nexit 0\n")

	out := captureStdout(t, func() {
		_ = runInfo(nil, []string{"wget"})
	})

	responses := decodeResponses(t, out)
	dataBytes, _ := json.Marshal(responses[0].Data)
	var fi contract.FormulaInfo
	json.Unmarshal(dataBytes, &fi)

	if fi.Installed {
		t.Error("formula with empty installed array should have Installed=false")
	}
	if fi.InstalledVersion != "" {
		t.Errorf("uninstalled formula should have empty InstalledVersion, got %q", fi.InstalledVersion)
	}
}

func TestRunInfo_Cask_WithName(t *testing.T) {
	t.Setenv("BREW_TUI_CACHE_DIR", t.TempDir())
	makeFakeBrew(t, "#!/bin/sh\nprintf '"+caskInfoJSON+"'\nexit 0\n")

	out := captureStdout(t, func() {
		_ = runInfo(nil, []string{"firefox"})
	})

	responses := decodeResponses(t, out)
	if !responses[0].Success || responses[0].Type != "info" {
		t.Errorf("expected success info, got success=%v type=%s", responses[0].Success, responses[0].Type)
	}

	dataBytes, _ := json.Marshal(responses[0].Data)
	var ci contract.CaskInfo
	if err := json.Unmarshal(dataBytes, &ci); err != nil {
		t.Fatalf("Data is not CaskInfo: %v", err)
	}
	if ci.Token != "firefox" {
		t.Errorf("expected token=firefox, got %s", ci.Token)
	}
	if ci.Name != "Mozilla Firefox" {
		t.Errorf("expected name=Mozilla Firefox, got %s", ci.Name)
	}
	if !ci.Installed {
		t.Error("cask with non-empty installed field should be Installed=true")
	}
}

func TestRunInfo_Cask_EmptyNameFallsBackToToken(t *testing.T) {
	t.Setenv("BREW_TUI_CACHE_DIR", t.TempDir())
	makeFakeBrew(t, "#!/bin/sh\nprintf '"+caskInfoNoNameJSON+"'\nexit 0\n")

	out := captureStdout(t, func() {
		_ = runInfo(nil, []string{"firefox"})
	})

	responses := decodeResponses(t, out)
	dataBytes, _ := json.Marshal(responses[0].Data)
	var ci contract.CaskInfo
	json.Unmarshal(dataBytes, &ci)

	if ci.Name != "firefox" {
		t.Errorf("empty name array should fall back to token, got Name=%q", ci.Name)
	}
}

func TestRunInfo_BrewError_EmitsErrorResponse(t *testing.T) {
	t.Setenv("BREW_TUI_CACHE_DIR", t.TempDir())
	makeFakeBrew(t, "#!/bin/sh\nexit 1\n")

	out := captureStdout(t, func() {
		_ = runInfo(nil, []string{"nonexistent-xyz"})
	})

	responses := decodeResponses(t, out)
	if len(responses) != 1 {
		t.Fatalf("expected 1 error response, got %d", len(responses))
	}
	if responses[0].Success || responses[0].Type != "error" {
		t.Errorf("expected error response, got success=%v type=%s", responses[0].Success, responses[0].Type)
	}
	if responses[0].Error == "" {
		t.Error("error field must be non-empty")
	}
}

func TestRunInfo_InvalidJSON_EmitsErrorResponse(t *testing.T) {
	t.Setenv("BREW_TUI_CACHE_DIR", t.TempDir())
	makeFakeBrew(t, "#!/bin/sh\nprintf 'this is not json'\nexit 0\n")

	out := captureStdout(t, func() {
		_ = runInfo(nil, []string{"wget"})
	})

	responses := decodeResponses(t, out)
	if responses[0].Success || responses[0].Type != "error" {
		t.Errorf("expected error on unmarshal failure, got success=%v type=%s", responses[0].Success, responses[0].Type)
	}
}

func TestRunInfo_NotFound_EmitsErrorResponse(t *testing.T) {
	t.Setenv("BREW_TUI_CACHE_DIR", t.TempDir())
	// brew succeeds but returns empty formulae and casks
	makeFakeBrew(t, "#!/bin/sh\nprintf '"+emptyInfoJSON+"'\nexit 0\n")

	out := captureStdout(t, func() {
		_ = runInfo(nil, []string{"phantom-package"})
	})

	responses := decodeResponses(t, out)
	if responses[0].Success || responses[0].Type != "error" {
		t.Errorf("expected not-found error, got success=%v type=%s", responses[0].Success, responses[0].Type)
	}
}

func TestRunInfo_WithLogger_CoversLoggerBranch(t *testing.T) {
	t.Setenv("BREW_TUI_CACHE_DIR", t.TempDir())
	rawOld, sugarOld := logger.Raw, logger.Sugar
	t.Cleanup(func() { logger.Raw = rawOld; logger.Sugar = sugarOld })

	t.Setenv("BREW_TUI_LOG_DIR", t.TempDir())
	if err := logger.Init(); err != nil {
		t.Fatal(err)
	}

	makeFakeBrew(t, "#!/bin/sh\nprintf '"+formulaInfoJSON+"'\nexit 0\n")

	out := captureStdout(t, func() {
		_ = runInfo(nil, []string{"wget"})
	})

	responses := decodeResponses(t, out)
	if len(responses) != 1 || !responses[0].Success {
		t.Errorf("expected success info response with logger active, got: %s", out)
	}
}

func TestRunInfo_AlwaysReturnsNilToCobraFromRunE(t *testing.T) {
	t.Setenv("BREW_TUI_CACHE_DIR", t.TempDir())
	makeFakeBrew(t, "#!/bin/sh\nexit 1\n")

	captureStdout(t, func() {
		err := runInfo(nil, []string{"pkg"})
		if err != nil {
			t.Errorf("RunE handler must return nil; got %v", err)
		}
	})
}

// ── cache paths ───────────────────────────────────────────────────────────────

func TestRunInfo_FreshCacheHit_ServesFromFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BREW_TUI_CACHE_DIR", dir)

	// Pre-seed a fresh cache entry.
	payload := `{"success":true,"type":"info","data":{"name":"wget","full_name":"wget","tap":"","desc":"","homepage":"","version":"","installed":false,"outdated":false,"pinned":false}}` + "\n"
	if err := cache.WriteInfo("wget", []byte(payload)); err != nil {
		t.Fatal(err)
	}

	// No brew in PATH — if brew were invoked the test would error.
	t.Setenv("PATH", t.TempDir())

	out := captureStdout(t, func() { _ = runInfo(nil, []string{"wget"}) })

	if out != payload {
		t.Errorf("expected raw cache bytes, got:\n%s", out)
	}
}

func TestRunInfo_FreshCacheHit_WithLogger_CoversDebugw(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BREW_TUI_CACHE_DIR", dir)
	rawOld, sugarOld := logger.Raw, logger.Sugar
	t.Cleanup(func() { logger.Raw = rawOld; logger.Sugar = sugarOld })
	t.Setenv("BREW_TUI_LOG_DIR", t.TempDir())
	if err := logger.Init(); err != nil {
		t.Fatal(err)
	}

	payload := `{"success":true,"type":"info","data":{"name":"wget","full_name":"wget","tap":"","desc":"","homepage":"","version":"","installed":false,"outdated":false,"pinned":false}}` + "\n"
	if err := cache.WriteInfo("wget", []byte(payload)); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", t.TempDir())

	out := captureStdout(t, func() { _ = runInfo(nil, []string{"wget"}) })

	if out != payload {
		t.Errorf("expected raw cache bytes with logger active, got:\n%s", out)
	}
}

func TestRunInfo_FreshCacheHit_WritesCacheFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BREW_TUI_CACHE_DIR", dir)
	makeFakeBrew(t, "#!/bin/sh\nprintf '"+formulaInfoJSON+"'\nexit 0\n")

	captureStdout(t, func() { _ = runInfo(nil, []string{"wget"}) })

	if _, err := os.Stat(filepath.Join(dir, "info", "wget.json")); os.IsNotExist(err) {
		t.Error("expected info/wget.json to be created after cache miss")
	}
}

func TestRunInfo_StaleCacheHit_EmitsTwoResponses(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BREW_TUI_CACHE_DIR", dir)
	makeFakeBrew(t, "#!/bin/sh\nprintf '"+formulaInfoJSON+"'\nexit 0\n")

	// Write a cache entry and then back-date it to 25 hours ago.
	payload := `{"success":true,"type":"info","data":{"name":"wget","full_name":"wget","tap":"","desc":"","homepage":"","version":"","installed":false,"outdated":false,"pinned":false}}` + "\n"
	if err := cache.WriteInfo("wget", []byte(payload)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "info", "wget.json")
	past := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() { _ = runInfo(nil, []string{"wget"}) })

	responses := decodeResponses(t, out)
	if len(responses) != 2 {
		t.Fatalf("stale path must emit 2 responses (stale + fresh), got %d:\n%s", len(responses), out)
	}
	if !responses[0].IsStale {
		t.Error("first response must have is_stale=true")
	}
	if responses[1].IsStale {
		t.Error("second (fresh) response must have is_stale=false/omitted")
	}
	if !responses[1].Success || responses[1].Type != "info" {
		t.Errorf("fresh response must be a success info, got success=%v type=%s", responses[1].Success, responses[1].Type)
	}
}

func TestRunInfo_ForceFlag_BypassesCache(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BREW_TUI_CACHE_DIR", dir)
	makeFakeBrew(t, "#!/bin/sh\nprintf '"+formulaInfoJSON+"'\nexit 0\n")

	// Write a fresh (non-stale) cache entry with stale-marker payload.
	stalePayload := `{"success":true,"type":"info","is_stale":true,"data":{"name":"stale","full_name":"stale","tap":"","desc":"","homepage":"","version":"","installed":false,"outdated":false,"pinned":false}}` + "\n"
	if err := cache.WriteInfo("wget", []byte(stalePayload)); err != nil {
		t.Fatal(err)
	}

	old := infoForceFlag
	infoForceFlag = true
	t.Cleanup(func() { infoForceFlag = old })

	out := captureStdout(t, func() { _ = runInfo(nil, []string{"wget"}) })

	responses := decodeResponses(t, out)
	if len(responses) != 1 {
		t.Fatalf("--force must emit exactly 1 fresh response, got %d: %s", len(responses), out)
	}
	if responses[0].IsStale {
		t.Error("--force response must not have is_stale=true")
	}
	dataBytes, _ := json.Marshal(responses[0].Data)
	var fi contract.FormulaInfo
	if err := json.Unmarshal(dataBytes, &fi); err != nil {
		t.Fatalf("expected FormulaInfo data: %v", err)
	}
	if fi.Name != "wget" {
		t.Errorf("expected fresh wget data, got name=%s", fi.Name)
	}
}

// ── injectIsStale ─────────────────────────────────────────────────────────────

func TestInjectIsStale_SetsFlag(t *testing.T) {
	input := `{"success":true,"type":"info","data":{"name":"wget"}}` + "\n"
	out := injectIsStale([]byte(input))

	var r contract.Response
	if err := json.Unmarshal([]byte(out[:len(out)-1]), &r); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if !r.IsStale {
		t.Error("injectIsStale must set IsStale=true")
	}
}

func TestInjectIsStale_MalformedInput_ReturnsOriginal(t *testing.T) {
	input := []byte("not-json\n")
	out := injectIsStale(input)
	if string(out) != string(input) {
		t.Error("malformed input must be returned unchanged")
	}
}

func TestRunInfo_WithLogger_StaleCacheHit_CoversLoggerBranch(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BREW_TUI_CACHE_DIR", dir)
	rawOld, sugarOld := logger.Raw, logger.Sugar
	t.Cleanup(func() { logger.Raw = rawOld; logger.Sugar = sugarOld })
	t.Setenv("BREW_TUI_LOG_DIR", t.TempDir())
	if err := logger.Init(); err != nil {
		t.Fatal(err)
	}
	makeFakeBrew(t, "#!/bin/sh\nprintf '"+formulaInfoJSON+"'\nexit 0\n")

	payload := `{"success":true,"type":"info","data":{"name":"wget","full_name":"wget","tap":"","desc":"","homepage":"","version":"","installed":false,"outdated":false,"pinned":false}}` + "\n"
	if err := cache.WriteInfo("wget", []byte(payload)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "info", "wget.json")
	past := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() { _ = runInfo(nil, []string{"wget"}) })

	responses := decodeResponses(t, out)
	if len(responses) != 2 {
		t.Fatalf("expected 2 responses for stale path with logger, got %d: %s", len(responses), out)
	}
}
