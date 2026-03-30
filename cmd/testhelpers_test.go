package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brewexplorer/brew-engine/internal/contract"
)

// makeFakeBrew writes a shell script named "brew" in a temp directory and
// prepends that directory to PATH for the duration of the test.
func makeFakeBrew(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "brew"), []byte(script), 0o755); err != nil {
		t.Fatalf("makeFakeBrew: %v", err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}

// captureStdout redirects os.Stdout to a pipe, calls f, then returns all
// bytes written to stdout. os.Stdout is restored before the function returns.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	f()
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	r.Close()
	return buf.String()
}

// decodeResponses splits raw output on newlines and unmarshals each non-empty
// line into a contract.Response, failing the test on any parse error.
func decodeResponses(t *testing.T, output string) []contract.Response {
	t.Helper()
	var out []contract.Response
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		var r contract.Response
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("invalid JSON line %q: %v", line, err)
		}
		out = append(out, r)
	}
	return out
}

// brewListScript returns a fake brew script that handles `list --formula`
// and `list --cask`, returning deterministic name lists.
func brewListScript() string {
	return `#!/bin/sh
if [ "$1" = "list" ] && [ "$2" = "--formula" ]; then
  printf "git\nwget\nzsh\n"
  exit 0
fi
if [ "$1" = "list" ] && [ "$2" = "--cask" ]; then
  printf "firefox\niterm2\n"
  exit 0
fi
exit 1
`
}

// brewListEmptyScript returns a fake brew that reports no packages installed.
func brewListEmptyScript() string {
	return `#!/bin/sh
printf ""
exit 0
`
}

// formulaInfoJSON is a minimal valid brew info --json=v2 response for wget.
const formulaInfoJSON = `{"formulae":[{"name":"wget","full_name":"wget","tap":"homebrew/core","desc":"Internet file retriever","homepage":"https://www.gnu.org/software/wget/","versions":{"stable":"1.25.0","head":"HEAD","bottle":true},"installed":[{"version":"1.25.0"}],"pinned":false,"outdated":false}],"casks":[]}`

// formulaInfoNotInstalledJSON is a formula with an empty installed array.
const formulaInfoNotInstalledJSON = `{"formulae":[{"name":"wget","full_name":"wget","tap":"homebrew/core","desc":"Internet file retriever","homepage":"https://www.gnu.org/software/wget/","versions":{"stable":"1.25.0","head":"HEAD","bottle":true},"installed":[],"pinned":false,"outdated":false}],"casks":[]}`

// caskInfoJSON is a minimal valid brew info --json=v2 response for firefox.
const caskInfoJSON = `{"formulae":[],"casks":[{"token":"firefox","full_token":"firefox","tap":"homebrew/cask","name":["Mozilla Firefox"],"desc":"Web browser","homepage":"https://www.mozilla.org/","version":"120.0","installed":"120.0","outdated":false}]}`

// caskInfoNoNameJSON is a cask whose name array is empty (falls back to token).
const caskInfoNoNameJSON = `{"formulae":[],"casks":[{"token":"firefox","full_token":"firefox","tap":"homebrew/cask","name":[],"desc":"Web browser","homepage":"https://www.mozilla.org/","version":"120.0","installed":"120.0","outdated":false}]}`

// emptyInfoJSON is a response with neither formulae nor casks.
const emptyInfoJSON = `{"formulae":[],"casks":[]}`
