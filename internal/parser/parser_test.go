package parser

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brewexplorer/brew-engine/internal/contract"
	"github.com/brewexplorer/brew-engine/internal/logger"
	"go.uber.org/zap"
)

// makeFakeBrew writes an executable shell script named "brew" into a temp
// directory and prepends that directory to PATH for the duration of the test.
func makeFakeBrew(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	brewPath := filepath.Join(dir, "brew")
	if err := os.WriteFile(brewPath, []byte(script), 0o755); err != nil {
		t.Fatalf("makeFakeBrew: %v", err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}

// saveLoggerState / restoreLoggerState bracket tests that touch logger globals.
func saveLoggerState() (*zap.Logger, *zap.SugaredLogger) { return logger.Raw, logger.Sugar }
func restoreLoggerState(r *zap.Logger, s *zap.SugaredLogger) {
	logger.Raw = r
	logger.Sugar = s
}

// decodeLines splits output into non-empty lines and decodes each as a
// contract.Response, failing the test if any line is not valid JSON.
func decodeLines(t *testing.T, output string) []contract.Response {
	t.Helper()
	var results []contract.Response
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		var r contract.Response
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("line %q is not valid JSON: %v", line, err)
		}
		results = append(results, r)
	}
	return results
}

// ── stripANSI ────────────────────────────────────────────────────────────────

func TestStripANSI_RemovesSGRCodes(t *testing.T) {
	cases := []struct{ input, want string }{
		{"\x1b[32mGreen\x1b[0m", "Green"},
		{"\x1b[1;31mBold Red\x1b[0m", "Bold Red"},
		{"\x1b[0m", ""},
	}
	for _, c := range cases {
		if got := stripANSI(c.input); got != c.want {
			t.Errorf("stripANSI(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestStripANSI_RemovesCursorCodes(t *testing.T) {
	input := "\x1b[2K\x1b[1A==> Installing"
	want := "==> Installing"
	if got := stripANSI(input); got != want {
		t.Errorf("stripANSI(%q) = %q, want %q", input, got, want)
	}
}

func TestStripANSI_NoOp_PlainText(t *testing.T) {
	input := "==> Downloading https://example.com/file.tar.gz"
	if got := stripANSI(input); got != input {
		t.Errorf("plain text should be unchanged: got %q", got)
	}
}

func TestStripANSI_EmptyString(t *testing.T) {
	if got := stripANSI(""); got != "" {
		t.Errorf("empty string should remain empty, got %q", got)
	}
}

func TestStripANSI_MultipleSequences(t *testing.T) {
	input := "\x1b[33m==>\x1b[0m \x1b[1mDownloading\x1b[0m file"
	want := "==> Downloading file"
	if got := stripANSI(input); got != want {
		t.Errorf("stripANSI(%q) = %q, want %q", input, got, want)
	}
}

// ── RunInstall ───────────────────────────────────────────────────────────────

func TestRunInstall_Success_EmitsProgressThenDone(t *testing.T) {
	makeFakeBrew(t, "#!/bin/sh\nprintf '==> Downloading wget\\n==> Installing wget\\nSome other line\\n'\nexit 0\n")

	var buf bytes.Buffer
	RunInstall("wget", &buf)

	events := decodeLines(t, buf.String())
	if len(events) != 3 {
		t.Fatalf("expected 3 events (2 progress + 1 done), got %d:\n%s", len(events), buf.String())
	}
	if events[0].Type != "progress" || !events[0].Success {
		t.Errorf("event[0]: expected progress/success, got type=%s success=%v", events[0].Type, events[0].Success)
	}
	if events[1].Type != "progress" || !events[1].Success {
		t.Errorf("event[1]: expected progress/success, got type=%s success=%v", events[1].Type, events[1].Success)
	}
	if events[2].Type != "done" || !events[2].Success {
		t.Errorf("event[2]: expected done/success, got type=%s success=%v", events[2].Type, events[2].Success)
	}
}

func TestRunInstall_NonZeroExit_EmitsErrorEvent(t *testing.T) {
	makeFakeBrew(t, "#!/bin/sh\nprintf '==> Downloading wget\\n'\nexit 2\n")

	var buf bytes.Buffer
	RunInstall("wget", &buf)

	events := decodeLines(t, buf.String())
	last := events[len(events)-1]
	if last.Success || last.Type != "error" {
		t.Errorf("expected final error event, got type=%s success=%v", last.Type, last.Success)
	}
}

func TestRunInstall_StartError_EmitsErrorEvent(t *testing.T) {
	// Point PATH to an empty dir so brew cannot be found.
	t.Setenv("PATH", t.TempDir())

	var buf bytes.Buffer
	RunInstall("wget", &buf)

	events := decodeLines(t, buf.String())
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 error event, got %d:\n%s", len(events), buf.String())
	}
	if events[0].Success || events[0].Type != "error" {
		t.Errorf("expected error event, got type=%s success=%v", events[0].Type, events[0].Success)
	}
}

func TestRunInstall_NoProgressLines_EmitsDoneOnly(t *testing.T) {
	makeFakeBrew(t, "#!/bin/sh\nprintf 'Pouring bottle\\n'\nexit 0\n")

	var buf bytes.Buffer
	RunInstall("wget", &buf)

	events := decodeLines(t, buf.String())
	if len(events) != 1 || events[0].Type != "done" {
		t.Errorf("expected single done event, got %d events: %s", len(events), buf.String())
	}
}

func TestRunInstall_ANSIStripped_InProgressStep(t *testing.T) {
	makeFakeBrew(t, "#!/bin/sh\nprintf '\\x1b[32m==> Downloading\\x1b[0m\\n'\nexit 0\n")

	var buf bytes.Buffer
	RunInstall("wget", &buf)

	events := decodeLines(t, buf.String())
	if events[0].Type != "progress" {
		t.Fatalf("expected progress event, got %s", events[0].Type)
	}
	// The Step field in the data must be ANSI-free
	raw, _ := json.Marshal(events[0].Data)
	if strings.Contains(string(raw), "\\u001b") || strings.Contains(string(raw), "\x1b") {
		t.Errorf("ANSI codes should be stripped from step text: %s", raw)
	}
}

// ── RunInstall with logger initialised ───────────────────────────────────────

func TestRunInstall_WithLogger_LogsBranchCovered(t *testing.T) {
	rawOld, sugarOld := saveLoggerState()
	t.Cleanup(func() { restoreLoggerState(rawOld, sugarOld) })

	t.Setenv("BREW_TUI_LOG_DIR", t.TempDir())
	if err := logger.Init(); err != nil {
		t.Fatal(err)
	}

	makeFakeBrew(t, "#!/bin/sh\nprintf '==> Installing wget\\nregular line\\n'\nexit 0\n")

	var buf bytes.Buffer
	RunInstall("wget", &buf) // logger.Sugar != nil branch exercised

	events := decodeLines(t, buf.String())
	if len(events) == 0 {
		t.Error("expected at least one event")
	}
}

// ── RunRemove ────────────────────────────────────────────────────────────────

func TestRunRemove_Success_EmitsProgressThenDone(t *testing.T) {
	makeFakeBrew(t, "#!/bin/sh\nprintf '==> Uninstalling wget\\n'\nexit 0\n")

	var buf bytes.Buffer
	RunRemove("wget", &buf)

	events := decodeLines(t, buf.String())
	if len(events) != 2 {
		t.Fatalf("expected 2 events (1 progress + 1 done), got %d:\n%s", len(events), buf.String())
	}
	if events[0].Type != "progress" {
		t.Errorf("event[0]: expected progress, got %s", events[0].Type)
	}
	if events[1].Type != "done" || !events[1].Success {
		t.Errorf("event[1]: expected done/success, got type=%s success=%v", events[1].Type, events[1].Success)
	}
}

func TestRunRemove_StartError_EmitsErrorEvent(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	var buf bytes.Buffer
	RunRemove("wget", &buf)

	events := decodeLines(t, buf.String())
	if len(events) != 1 || events[0].Type != "error" {
		t.Errorf("expected single error event, got %d events: %s", len(events), buf.String())
	}
}

func TestRunRemove_NonZeroExit_EmitsErrorEvent(t *testing.T) {
	makeFakeBrew(t, "#!/bin/sh\nexit 1\n")

	var buf bytes.Buffer
	RunRemove("nonexistent", &buf)

	events := decodeLines(t, buf.String())
	last := events[len(events)-1]
	if last.Success || last.Type != "error" {
		t.Errorf("expected error event on non-zero exit, got type=%s success=%v", last.Type, last.Success)
	}
}

// ── Pipe error paths (via execBrewCommand override) ──────────────────────────

func TestRunStreamingCommand_StdoutPipeError_EmitsErrorEvent(t *testing.T) {
	orig := execBrewCommand
	t.Cleanup(func() { execBrewCommand = orig })

	// Pre-set Stdout so that StdoutPipe() returns "Stdout already set" error.
	execBrewCommand = func(_ []string) *exec.Cmd {
		cmd := exec.Command("echo", "test")
		cmd.Stdout = os.Stdout
		return cmd
	}

	var buf bytes.Buffer
	RunInstall("wget", &buf)

	events := decodeLines(t, buf.String())
	if len(events) != 1 || events[0].Type != "error" || events[0].Success {
		t.Errorf("expected single error event for StdoutPipe failure, got %d events: %s", len(events), buf.String())
	}
	if events[0].Error == "" {
		t.Error("error field must not be empty")
	}
}

func TestRunStreamingCommand_StderrPipeError_EmitsErrorEvent(t *testing.T) {
	orig := execBrewCommand
	t.Cleanup(func() { execBrewCommand = orig })

	// Leave Stdout nil (StdoutPipe succeeds) but pre-set Stderr so StderrPipe fails.
	execBrewCommand = func(_ []string) *exec.Cmd {
		cmd := exec.Command("echo", "test")
		cmd.Stderr = os.Stderr
		return cmd
	}

	var buf bytes.Buffer
	RunRemove("wget", &buf)

	events := decodeLines(t, buf.String())
	if len(events) != 1 || events[0].Type != "error" || events[0].Success {
		t.Errorf("expected single error event for StderrPipe failure, got %d events: %s", len(events), buf.String())
	}
	if events[0].Error == "" {
		t.Error("error field must not be empty")
	}
}

// ── detectBuildMode ──────────────────────────────────────────────────────────

func TestDetectBuildMode_BottlePouring(t *testing.T) {
	cases := []string{
		"==> Pouring wget--1.21.4.arm64_ventura.bottle.tar.gz",
		"==> POURING wget--1.0.bottle.tar.gz", // case-insensitive
	}
	for _, line := range cases {
		if got := detectBuildMode(line); got != buildModeBottle {
			t.Errorf("detectBuildMode(%q) = %d, want buildModeBottle", line, got)
		}
	}
}

func TestDetectBuildMode_SourceIndicators(t *testing.T) {
	cases := []string{
		"==> Installing dependencies for wget: gettext, libidn2",
		"==> Installing wget dependency: gettext",
		"==> ./configure --prefix=/opt/homebrew",
		"==> cmake -DCMAKE_BUILD_TYPE=Release",
		"==> make install",
	}
	for _, line := range cases {
		if got := detectBuildMode(line); got != buildModeSource {
			t.Errorf("detectBuildMode(%q) = %d, want buildModeSource", line, got)
		}
	}
}

func TestDetectBuildMode_NonDecisive(t *testing.T) {
	cases := []string{
		"==> Downloading https://ghcr.io/v2/homebrew/core/wget",
		"==> Installing wget",
		"==> Uninstalling wget",
		"Some regular output line",
		"",
	}
	for _, line := range cases {
		if got := detectBuildMode(line); got != buildModeUnknown {
			t.Errorf("detectBuildMode(%q) = %d, want buildModeUnknown", line, got)
		}
	}
}

func TestBuildModeString(t *testing.T) {
	if got := buildModeString(buildModeBottle); got != "bottle" {
		t.Errorf("buildModeString(bottle) = %q, want \"bottle\"", got)
	}
	if got := buildModeString(buildModeSource); got != "source" {
		t.Errorf("buildModeString(source) = %q, want \"source\"", got)
	}
	if got := buildModeString(buildModeUnknown); got != "" {
		t.Errorf("buildModeString(unknown) = %q, want empty", got)
	}
}

// ── Build-mode events (bottle path) ──────────────────────────────────────────

func TestRunInstall_BottleInstall_EmitsBuildModeBottle(t *testing.T) {
	script := "#!/bin/sh\n" +
		"printf '==> Downloading https://ghcr.io/v2/homebrew/core/wget\\n'\n" +
		"printf '==> Pouring wget--1.21.4.arm64_ventura.bottle.tar.gz\\n'\n" +
		"exit 0\n"
	makeFakeBrew(t, script)

	var buf bytes.Buffer
	RunInstall("wget", &buf)

	events := decodeLines(t, buf.String())

	var bmEvent *contract.Response
	for i := range events {
		if events[i].Type == "build_mode" {
			e := events[i]
			bmEvent = &e
			break
		}
	}
	if bmEvent == nil {
		t.Fatalf("no build_mode event in output:\n%s", buf.String())
	}
	if !bmEvent.Success {
		t.Error("build_mode event should have success=true")
	}

	dataBytes, _ := json.Marshal(bmEvent.Data)
	var bmd contract.BuildModeData
	if err := json.Unmarshal(dataBytes, &bmd); err != nil {
		t.Fatalf("cannot decode BuildModeData: %v", err)
	}
	if bmd.Package != "wget" {
		t.Errorf("BuildModeData.Package = %q, want \"wget\"", bmd.Package)
	}
	if bmd.Mode != "bottle" {
		t.Errorf("BuildModeData.Mode = %q, want \"bottle\"", bmd.Mode)
	}
}

func TestRunInstall_BottleInstall_DoneCarriesBuildMode(t *testing.T) {
	script := "#!/bin/sh\n" +
		"printf '==> Pouring wget--1.21.4.arm64_ventura.bottle.tar.gz\\n'\n" +
		"exit 0\n"
	makeFakeBrew(t, script)

	var buf bytes.Buffer
	RunInstall("wget", &buf)

	events := decodeLines(t, buf.String())
	doneEvent := events[len(events)-1]
	if doneEvent.Type != "done" {
		t.Fatalf("last event should be done, got %s", doneEvent.Type)
	}

	dataBytes, _ := json.Marshal(doneEvent.Data)
	var dd contract.DoneData
	if err := json.Unmarshal(dataBytes, &dd); err != nil {
		t.Fatalf("cannot decode DoneData: %v", err)
	}
	if dd.BuildMode != "bottle" {
		t.Errorf("DoneData.BuildMode = %q, want \"bottle\"", dd.BuildMode)
	}
}

// ── Build-mode events (source path) ──────────────────────────────────────────

func TestRunInstall_SourceBuild_EmitsBuildModeSource(t *testing.T) {
	script := "#!/bin/sh\n" +
		"printf '==> Installing dependencies for wget: gettext\\n'\n" +
		"printf '==> Installing wget dependency: gettext\\n'\n" +
		"printf '==> ./configure --prefix=/opt/homebrew\\n'\n" +
		"exit 0\n"
	makeFakeBrew(t, script)

	var buf bytes.Buffer
	RunInstall("wget", &buf)

	events := decodeLines(t, buf.String())

	// Exactly one build_mode event must be emitted (deduplicated by CAS).
	var bmEvents []contract.Response
	for _, e := range events {
		if e.Type == "build_mode" {
			bmEvents = append(bmEvents, e)
		}
	}
	if len(bmEvents) != 1 {
		t.Fatalf("expected exactly 1 build_mode event, got %d:\n%s", len(bmEvents), buf.String())
	}

	dataBytes, _ := json.Marshal(bmEvents[0].Data)
	var bmd contract.BuildModeData
	if err := json.Unmarshal(dataBytes, &bmd); err != nil {
		t.Fatalf("cannot decode BuildModeData: %v", err)
	}
	if bmd.Mode != "source" {
		t.Errorf("BuildModeData.Mode = %q, want \"source\"", bmd.Mode)
	}
}

func TestRunInstall_SourceBuild_DoneCarriesBuildMode(t *testing.T) {
	script := "#!/bin/sh\n" +
		"printf '==> Installing dependencies for wget: gettext\\n'\n" +
		"exit 0\n"
	makeFakeBrew(t, script)

	var buf bytes.Buffer
	RunInstall("wget", &buf)

	events := decodeLines(t, buf.String())
	doneEvent := events[len(events)-1]
	if doneEvent.Type != "done" {
		t.Fatalf("last event should be done, got %s", doneEvent.Type)
	}

	dataBytes, _ := json.Marshal(doneEvent.Data)
	var dd contract.DoneData
	if err := json.Unmarshal(dataBytes, &dd); err != nil {
		t.Fatalf("cannot decode DoneData: %v", err)
	}
	if dd.BuildMode != "source" {
		t.Errorf("DoneData.BuildMode = %q, want \"source\"", dd.BuildMode)
	}
}

func TestRunInstall_NoBuildModeDetected_DoneHasEmptyBuildMode(t *testing.T) {
	makeFakeBrew(t, "#!/bin/sh\nprintf '==> Downloading wget\\n'\nexit 0\n")

	var buf bytes.Buffer
	RunInstall("wget", &buf)

	events := decodeLines(t, buf.String())
	for _, e := range events {
		if e.Type == "build_mode" {
			t.Error("unexpected build_mode event for non-decisive output")
		}
	}

	doneEvent := events[len(events)-1]
	dataBytes, _ := json.Marshal(doneEvent.Data)
	var dd contract.DoneData
	_ = json.Unmarshal(dataBytes, &dd)
	if dd.BuildMode != "" {
		t.Errorf("DoneData.BuildMode should be empty, got %q", dd.BuildMode)
	}
}

func TestRunRemove_NoBuildModeEvent(t *testing.T) {
	makeFakeBrew(t, "#!/bin/sh\nprintf '==> Uninstalling wget\\n'\nexit 0\n")

	var buf bytes.Buffer
	RunRemove("wget", &buf)

	for _, e := range decodeLines(t, buf.String()) {
		if e.Type == "build_mode" {
			t.Error("remove operation must not emit a build_mode event")
		}
	}
}

// ── DoneData payload verification ────────────────────────────────────────────

func TestRunInstall_DoneData_ContainsPackageAndExitCode(t *testing.T) {
	makeFakeBrew(t, "#!/bin/sh\nexit 0\n")

	var buf bytes.Buffer
	RunInstall("wget", &buf)

	events := decodeLines(t, buf.String())
	doneEvent := events[len(events)-1]

	dataBytes, _ := json.Marshal(doneEvent.Data)
	var dd contract.DoneData
	if err := json.Unmarshal(dataBytes, &dd); err != nil {
		t.Fatalf("cannot decode DoneData: %v", err)
	}
	if dd.Package != "wget" {
		t.Errorf("DoneData.Package = %q, want wget", dd.Package)
	}
	if dd.ExitCode != 0 {
		t.Errorf("DoneData.ExitCode = %d, want 0", dd.ExitCode)
	}
}

func TestRunInstall_ErrorData_ContainsNonZeroExitCode(t *testing.T) {
	makeFakeBrew(t, "#!/bin/sh\nexit 42\n")

	var buf bytes.Buffer
	RunInstall("wget", &buf)

	events := decodeLines(t, buf.String())
	errEvent := events[len(events)-1]

	dataBytes, _ := json.Marshal(errEvent.Data)
	var dd contract.DoneData
	if err := json.Unmarshal(dataBytes, &dd); err != nil {
		t.Fatalf("cannot decode DoneData from error event: %v", err)
	}
	if dd.ExitCode != 42 {
		t.Errorf("DoneData.ExitCode = %d, want 42", dd.ExitCode)
	}
}
