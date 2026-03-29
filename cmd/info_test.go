package cmd

import (
	"encoding/json"
	"testing"

	"github.com/brewexplorer/brew-engine/internal/contract"
	"github.com/brewexplorer/brew-engine/internal/logger"
)

// ── runInfo ──────────────────────────────────────────────────────────────────

func TestRunInfo_Formula_Installed(t *testing.T) {
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
	// Initialise the logger so the logger.Sugar != nil branch in runInfo is hit.
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
	makeFakeBrew(t, "#!/bin/sh\nexit 1\n")

	captureStdout(t, func() {
		err := runInfo(nil, []string{"pkg"})
		if err != nil {
			t.Errorf("RunE handler must return nil; got %v", err)
		}
	})
}
