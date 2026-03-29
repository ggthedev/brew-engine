package cmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// ── Execute (success path) ───────────────────────────────────────────────────

func TestExecute_KnownSubcommand_DoesNotPanic(t *testing.T) {
	// Override os.Args so Cobra sees "brew-engine list"
	// Use a fake brew so list completes cleanly.
	makeFakeBrew(t, brewListScript())

	old := os.Args
	os.Args = []string{"brew-engine", "list"}
	t.Cleanup(func() { os.Args = old })

	captureStdout(t, func() {
		Execute() // must not panic or os.Exit
	})
}

func TestExecute_NoSubcommand_EmitsJSONError(t *testing.T) {
	// rootCmd.RunE emits a JSON error when called with no subcommand,
	// preserving the stdout JSON-only invariant.
	old := os.Args
	os.Args = []string{"brew-engine"}
	t.Cleanup(func() { os.Args = old })

	out := captureStdout(t, func() {
		Execute()
	})

	responses := decodeResponses(t, out)
	if len(responses) != 1 {
		t.Fatalf("expected exactly 1 JSON response, got %d:\n%s", len(responses), out)
	}
	if responses[0].Success {
		t.Error("no-subcommand response should have success=false")
	}
	if responses[0].Type != "error" {
		t.Errorf("expected type=error, got %s", responses[0].Type)
	}
}

// ── Execute (error path via subprocess) ──────────────────────────────────────
//
// Execute calls os.Exit(1) on an unknown command, which would terminate the
// test process if called directly. We run it in a subprocess instead.

func TestExecute_UnknownCommand_ExitsWithCodeOne(t *testing.T) {
	if os.Getenv("BE_TEST_HELPER") == "1" {
		// We are the subprocess: invoke Execute with a bad command and let it exit.
		os.Args = []string{"brew-engine", "no-such-command-xyz"}
		Execute()
		return
	}

	// Parent: re-run this specific test inside a subprocess.
	cmd := exec.Command(os.Args[0], "-test.run=TestExecute_UnknownCommand_ExitsWithCodeOne", "-test.v")
	cmd.Env = append(os.Environ(), "BE_TEST_HELPER=1")
	out, err := cmd.CombinedOutput()

	// The subprocess must exit non-zero.
	if err == nil {
		t.Errorf("expected non-zero exit for unknown command, got nil error; output:\n%s", out)
	}

	// stdout of the subprocess should contain a JSON error payload.
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Look for a JSON object with "success":false
		if strings.HasPrefix(line, "{") {
			var r map[string]interface{}
			if err := json.Unmarshal([]byte(line), &r); err == nil {
				if success, ok := r["success"].(bool); ok && !success {
					return // found the expected error payload
				}
			}
		}
	}
	t.Errorf("expected a JSON error payload in output, got:\n%s", out)
}
