// Package parser executes state-changing brew subcommands (install, remove)
// and translates their raw text output into the engine's JSON contract.
//
// # Why a dedicated parser package?
//
// Homebrew does not emit machine-readable output for mutations. Instead it
// streams human-readable text interspersed with ANSI escape sequences for
// colour and cursor movement. A naive approach of capturing the full output
// and returning it at the end would block the frontend until the operation
// completes — which can take minutes for large packages.
//
// # Stream processing model
//
// The parser attaches to both the stdout and stderr pipes of the brew
// subprocess before it starts. Two goroutines drain the pipes line-by-line
// using [bufio.Scanner]. For every line:
//
//  1. ANSI escape sequences are stripped with a precompiled regexp.
//  2. The clean line is appended to the background log file via [logger.Sugar].
//  3. If the line begins with "==>", a [contract.ProgressStep] JSON event is
//     written to the caller-supplied [io.Writer] (os.Stdout in production).
//
// The main goroutine blocks on a buffered done channel until both scanner
// goroutines have finished, then calls cmd.Wait() to collect the exit code
// and emits a final [contract.DoneData] event.
//
// # Synchronous-by-design
//
// Homebrew enforces a system-wide lock during mutations; there is therefore
// no benefit to running multiple install operations concurrently. The entire
// package is intentionally synchronous — one command at a time.
package parser

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"

	"github.com/brewexplorer/brew-engine/internal/contract"
	"github.com/brewexplorer/brew-engine/internal/logger"
)

// ansiEscape is a precompiled regular expression that matches ANSI/VT100
// Control Sequence Introducer (CSI) escape sequences of the form:
//
//	ESC [ <params> <final-byte>
//
// where <params> is a sequence of digits, semicolons, and question marks,
// and <final-byte> is any ASCII letter. This pattern covers the sequences
// that Homebrew emits: SGR colour codes (e.g. \x1b[32m), cursor movement
// (e.g. \x1b[1A), and erase-in-line (e.g. \x1b[2K).
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

// execBrewCommand constructs the exec.Cmd that runStreamingCommand will start.
// It is a package-level variable so tests can override it to return a
// pre-configured *exec.Cmd that triggers error paths (e.g. pre-set Stdout
// to force StdoutPipe to return an error).
var execBrewCommand = func(brewArgs []string) *exec.Cmd {
	return exec.Command("brew", brewArgs...)
}

// stripANSI returns a copy of s with all ANSI escape sequences removed.
// It uses [ansiEscape] and is called on every line captured from the brew
// subprocess before the line is logged or compared against the "==>" prefix.
func stripANSI(s string) string {
	return ansiEscape.ReplaceAllString(s, "")
}

// RunInstall executes `brew install -v <pkg>` and streams the output as
// newline-delimited JSON events to out.
//
// The -v (verbose) flag is passed to brew so that every "==>" phase header
// is emitted to stdout rather than being suppressed. Without it, silent
// bottle-pour installs would produce no progress events at all.
//
// Output written to out:
//   - Zero or more Type="progress" events, one per "==>" line encountered.
//   - A single terminal event: Type="done" on success, or Type="error" on
//     non-zero exit. Both carry a [contract.DoneData] payload.
func RunInstall(pkg string, out io.Writer) {
	runStreamingCommand(pkg, []string{"install", "-v", pkg}, out)
}

// RunRemove executes `brew uninstall <pkg>` and streams the output as
// newline-delimited JSON events to out.
//
// Output written to out:
//   - Zero or more Type="progress" events, one per "==>" line encountered.
//   - A single terminal event: Type="done" on success, or Type="error" on
//     non-zero exit. Both carry a [contract.DoneData] payload.
func RunRemove(pkg string, out io.Writer) {
	runStreamingCommand(pkg, []string{"uninstall", pkg}, out)
}

// runStreamingCommand is the shared implementation for [RunInstall] and
// [RunRemove]. It launches `brew <brewArgs...>`, attaches to its output
// pipes, and translates the stream into contract JSON events on out.
//
// Parameters:
//   - pkg: the formula or cask name, used to populate payload fields.
//   - brewArgs: the full argument list passed to the brew executable
//     (e.g. ["install", "-v", "wget"] or ["uninstall", "wget"]).
//   - out: destination writer for JSON events; in production this is
//     os.Stdout, but tests may supply any io.Writer.
//
// Concurrency note: the two scanner goroutines both call [contract.WriteJSON]
// on the shared out writer. This is safe on macOS because os.File.Write is
// backed by a single write(2) syscall, which is atomic for payloads well
// below PIPE_BUF (512 bytes on macOS; our JSON lines are ~120 bytes).
//
// Deadlock prevention: cmd.Wait() is called only after both scanner
// goroutines have signalled completion via the done channel. Calling Wait
// before draining the pipes would deadlock once the pipe buffer fills.
func runStreamingCommand(pkg string, brewArgs []string, out io.Writer) {
	cmd := execBrewCommand(brewArgs)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		contract.WriteJSON(out, contract.Response{
			Success: false,
			Type:    "error",
			Error:   fmt.Sprintf("stdout pipe: %s", err),
		})
		return
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		contract.WriteJSON(out, contract.Response{
			Success: false,
			Type:    "error",
			Error:   fmt.Sprintf("stderr pipe: %s", err),
		})
		return
	}

	if err := cmd.Start(); err != nil {
		contract.WriteJSON(out, contract.Response{
			Success: false,
			Type:    "error",
			Error:   fmt.Sprintf("failed to start brew: %s", err),
		})
		return
	}

	done := make(chan struct{}, 2)

	// scanStream is an inline closure that drains a single pipe from the brew
	// subprocess. It is launched as a goroutine for each of stdout and stderr.
	// The source parameter ("stdout" or "stderr") is attached to every log
	// entry so that post-mortem analysis can distinguish the two streams.
	scanStream := func(r io.Reader, source string) {
		defer func() { done <- struct{}{} }()

		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			raw := scanner.Text()
			clean := strings.TrimRight(stripANSI(raw), "\r")

			// ── Log every line (raw brew output goes here, never to stdout) ──
			if logger.Sugar != nil {
				logger.Sugar.Infow("brew output",
					"source", source,
					"package", pkg,
					"line", clean,
				)
			}

			// ── Emit a progress event for `==>` step-header lines only ──────
			if strings.HasPrefix(clean, "==>") {
				contract.WriteJSON(out, contract.Response{
					Success: true,
					Type:    "progress",
					Data: contract.ProgressStep{
						Package: pkg,
						Step:    clean,
					},
				})
			}
		}

		if err := scanner.Err(); err != nil && logger.Sugar != nil {
			logger.Sugar.Warnw("scanner error", "source", source, "error", err)
		}
	}

	go scanStream(stdoutPipe, "stdout")
	go scanStream(stderrPipe, "stderr")

	// Wait for both scanner goroutines to finish draining their pipes.
	// The done channel has capacity 2, so neither goroutine blocks on send.
	// We receive twice — once per goroutine — before proceeding to Wait.
	<-done
	<-done

	exitCode := 0
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	doneData := contract.DoneData{Package: pkg, ExitCode: exitCode}

	if exitCode == 0 {
		contract.WriteJSON(out, contract.Response{
			Success: true,
			Type:    "done",
			Data:    doneData,
		})
	} else {
		contract.WriteJSON(out, contract.Response{
			Success: false,
			Type:    "error",
			Error:   fmt.Sprintf("brew exited with code %d", exitCode),
			Data:    doneData,
		})
	}
}
