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
	"sync/atomic"

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

// buildMode sentinel values used by the atomic flag below.
const (
	buildModeUnknown = int32(0)
	buildModeBottle  = int32(1)
	buildModeSource  = int32(2)
)

// buildModeString converts an atomic sentinel to the JSON string value.
func buildModeString(m int32) string {
	switch m {
	case buildModeBottle:
		return "bottle"
	case buildModeSource:
		return "source"
	default:
		return ""
	}
}

// buildModeRule pairs a required line prefix with an optional substring that
// must also be present, and the build mode to return when both match.
// Rules are evaluated in declaration order; the first match wins.
type buildModeRule struct {
	prefix   string
	contains string
	mode     int32
}

// buildModeRules is the ordered set of heuristics used by [detectBuildMode].
// To support a new brew output pattern, append a row here — no other code
// needs to change.
var buildModeRules = []buildModeRule{
	{prefix: "==> pouring", contains: ".bottle.", mode: buildModeBottle},
	{prefix: "==> installing dependencies for", mode: buildModeSource},
	{prefix: "==> installing", contains: " dependency:", mode: buildModeSource},
	{prefix: "==> ./configure", mode: buildModeSource},
	{prefix: "==> cmake", mode: buildModeSource},
	{prefix: "==> make", mode: buildModeSource},
}

// detectBuildMode inspects a clean (ANSI-stripped) "==>" line and returns
// the build mode it implies, or buildModeUnknown when no rule matches.
func detectBuildMode(line string) int32 {
	lower := strings.ToLower(line)
	for _, r := range buildModeRules {
		if strings.HasPrefix(lower, r.prefix) {
			if r.contains == "" || strings.Contains(lower, r.contains) {
				return r.mode
			}
		}
	}
	return buildModeUnknown
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

	// Open brew-output.log for raw subprocess output (best-effort; nil means disabled).
	brewLog := logger.OpenBrewOutputLog("brew " + strings.Join(brewArgs, " "))

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

	// buildModeFlag is shared between the two scanStream goroutines.
	// Only the first goroutine to observe a decisive ==> line will win the
	// CompareAndSwap and emit the build_mode event; subsequent calls are
	// no-ops. Using atomic avoids a mutex on the hot scanner path.
	var buildModeFlag atomic.Int32 // zero value == buildModeUnknown

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

			// ── Tee raw line to brew-output.log ───────────────────────────
			if brewLog != nil {
				_, _ = fmt.Fprintln(brewLog, raw)
			}

			// ── Log every line (structured log, never to stdout) ─────────
			// This is debug-level: trace every instruction when support mode is on.
			if logger.Sugar != nil {
				logger.Sugar.Debugw("brew output",
					"source", source,
					"package", pkg,
					"line", clean,
				)
			}

			if strings.HasPrefix(clean, "==>") {
				// ── Build-mode detection (once per install) ──────────────
				// Try to determine bottle vs. source from this ==> line.
				// The first goroutine to observe a decisive indicator wins the
				// CAS; all subsequent calls see a non-zero flag and skip.
				if detected := detectBuildMode(clean); detected != buildModeUnknown {
					if buildModeFlag.CompareAndSwap(buildModeUnknown, detected) {
						mode := buildModeString(detected)
						if logger.Sugar != nil {
							logger.Sugar.Infow("build mode detected",
								"package", pkg,
								"mode", mode,
							)
						}
						contract.WriteJSON(out, contract.Response{
							Success: true,
							Type:    "build_mode",
							Data: contract.BuildModeData{
								Package: pkg,
								Mode:    mode,
							},
						})
					}
				}

				// ── Emit a progress event for every ==> step-header line ─────
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

	// Write footer and close the raw brew output log.
	logger.WriteBrewOutputFooter(brewLog, exitCode)
	if brewLog != nil {
		_ = brewLog.Close()
	}

	doneData := contract.DoneData{
		Package:   pkg,
		ExitCode:  exitCode,
		BuildMode: buildModeString(buildModeFlag.Load()),
	}

	// Key info marker: command completion with outcome.
	if logger.Sugar != nil {
		logger.Sugar.Infow("command completed",
			"package", pkg,
			"exit_code", exitCode,
			"build_mode", doneData.BuildMode,
		)
	}

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
