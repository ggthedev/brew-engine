// Package parser handles all state-changing brew subcommands (install, remove).
//
// Design rationale:
//   - brew does not emit JSON for installs; it streams human-readable text with
//     ANSI escape codes for progress bars and colour.
//   - We must not block stdout until the command finishes — the frontend needs
//     incremental progress to render a live modal overlay.
//   - Solution: attach to both stdout and stderr pipes, drain them concurrently
//     with bufio.Scanner, strip ANSI codes, log every raw line, and emit one
//     JSON progress event per `==>` step line.
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

// ansiEscape matches ANSI/VT100 CSI escape sequences (colours, cursor moves,
// erase sequences) that Homebrew injects into its progress output.
// This covers the vast majority of brew's terminal output.
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

// stripANSI removes all matched ANSI escape sequences from s.
func stripANSI(s string) string {
	return ansiEscape.ReplaceAllString(s, "")
}

// RunInstall executes `brew install -v <pkg>`.
func RunInstall(pkg string, out io.Writer) {
	runStreamingCommand(pkg, []string{"install", "-v", pkg}, out)
}

// RunRemove executes `brew uninstall <pkg>`.
func RunRemove(pkg string, out io.Writer) {
	runStreamingCommand(pkg, []string{"uninstall", pkg}, out)
}

// runStreamingCommand is the shared engine for all state-changing brew calls.
// It is intentionally synchronous — Homebrew holds a system-wide lock during
// mutations, so there is no value in parallelism here.
//
// Stream lifecycle:
//  1. Start the subprocess and obtain stdout + stderr pipes.
//  2. Two goroutines drain the pipes concurrently via bufio.Scanner.
//  3. Both goroutines share the same out writer; each json.Marshal+Fprintln
//     is a single write syscall, which is atomic for payloads < PIPE_BUF.
//  4. The main goroutine blocks on a done channel until both scanners finish,
//     then calls cmd.Wait() to collect the exit code.
//  5. A final "done" or "error" JSON event is emitted.
func runStreamingCommand(pkg string, brewArgs []string, out io.Writer) {
	cmd := exec.Command("brew", brewArgs...)

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

	// scanStream drains one pipe. source is "stdout" or "stderr" for log context.
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

	// Block until both pipes are fully drained before calling Wait().
	// Calling Wait() before draining causes a deadlock on full pipe buffers.
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
