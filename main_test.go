package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain_HappyPath exercises the main() function's success path:
// logger.Init succeeds → defer logger.Sync registered → cmd.Execute runs.
// os.Exit is NOT triggered on this path so it is safe to call main() directly.
func TestMain_HappyPath(t *testing.T) {
	// Set up a writable log directory.
	t.Setenv("BREW_TUI_LOG_DIR", t.TempDir())
	// Isolate the cache so the fake-brew output does not persist to the real
	// ~/Library/Application Support/BrewExplorer/cache directory.
	t.Setenv("BREW_TUI_CACHE_DIR", t.TempDir())

	// Provide a fake brew that satisfies the list subcommand.
	brewDir := t.TempDir()
	brewScript := "#!/bin/sh\nprintf 'git\\nwget\\n'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(brewDir, "brew"), []byte(brewScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", brewDir+":"+os.Getenv("PATH"))

	// Route Cobra to the list subcommand.
	oldArgs := os.Args
	os.Args = []string{"brew-engine", "list"}
	t.Cleanup(func() { os.Args = oldArgs })

	// Capture stdout so the test output stays clean.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	os.Stdout = w

	main()

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	io.Copy(&buf, r)
	r.Close()

	if !strings.Contains(buf.String(), `"type":"list"`) {
		t.Errorf("expected a list JSON response from main(), got: %s", buf.String())
	}
}

// TestMain_LoggerInitError_WritesToStderr exercises the error branch of main()
// (logger.Init fails → fmt.Fprintf to stderr → os.Exit) via a subprocess so
// that os.Exit does not terminate the test process.
func TestMain_LoggerInitError_ExitsNonZero(t *testing.T) {
	if os.Getenv("BE_MAIN_FAIL_TEST") == "1" {
		// Subprocess: point log dir to a file (not a dir) so Init fails.
		tmpFile, _ := os.CreateTemp("", "brew-logger-fail-*")
		tmpFile.Close()
		os.Setenv("BREW_ENGINE_LOG_DIR", filepath.Join(tmpFile.Name(), "subdir"))
		main()
		return
	}

	ps := runSubprocess(t, "TestMain_LoggerInitError_ExitsNonZero", "BE_MAIN_FAIL_TEST=1")
	if ps.ExitCode() == 0 {
		t.Error("expected non-zero exit when logger.Init fails")
	}
}

// TestMain_BrewNotFound_ExitsCode2EmitsJSON verifies that when brew is absent
// the engine emits a brew_not_found JSON line on stdout and exits with code 2.
// The audit log entry is also written to the log directory.
func TestMain_BrewNotFound_ExitsCode2EmitsJSON(t *testing.T) {
	if os.Getenv("BE_BREW_NOT_FOUND_TEST") == "1" {
		// Subprocess body: BREW_ENGINE_BREW_PATH is pre-set to a non-existent
		// path so config.Load() leaves it as-is and IsBrewExecutable returns false.
		main()
		return
	}

	logDir := t.TempDir()

	// exec.Command is used instead of StartProcess so we can capture stdout.
	cmd := exec.Command(os.Args[0],
		"-test.run=^TestMain_BrewNotFound_ExitsCode2EmitsJSON$",
		"-test.v",
	)
	cmd.Env = append(os.Environ(),
		"BE_BREW_NOT_FOUND_TEST=1",
		"BREW_ENGINE_BREW_PATH=/nonexistent/does/not/exist/brew",
		"BREW_ENGINE_LOG_DIR="+logDir,
	)

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard

	err := cmd.Run()

	exitCode := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	}
	if exitCode != 2 {
		t.Errorf("expected exit code 2, got %d (stdout: %s)", exitCode, stdout.String())
	}

	out := stdout.String()
	if !strings.Contains(out, `"type":"brew_not_found"`) {
		t.Errorf("expected brew_not_found JSON on stdout, got: %s", out)
	}
	if !strings.Contains(out, `"success":false`) {
		t.Errorf("expected success:false in JSON, got: %s", out)
	}
	if !strings.Contains(out, `"checked_paths"`) {
		t.Errorf("expected checked_paths in JSON data, got: %s", out)
	}
}

// runSubprocess re-executes the current test binary running only the named
// test, with the supplied extra environment variables appended.
func runSubprocess(t *testing.T, testName string, extraEnv ...string) *os.ProcessState {
	t.Helper()
	c := make([]string, 0, len(os.Environ())+len(extraEnv))
	c = append(c, os.Environ()...)
	c = append(c, extraEnv...)

	proc, err := os.StartProcess(os.Args[0],
		[]string{os.Args[0], "-test.run=^" + testName + "$", "-test.v"},
		&os.ProcAttr{
			Env:   c,
			Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
		},
	)
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	ps, err := proc.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	return ps
}
