package main

import (
	"bytes"
	"io"
	"os"
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
