// Package logger configures and exposes the global Zap-backed file logger
// used by all brew-engine subcommands and the parser.
//
// # stdout exclusion
//
// stdout is owned exclusively by [contract.WriteJSON] for JSON payloads.
// This package writes ONLY to a background log file — never to stdout or
// stderr — so that the JSON channel is never contaminated by diagnostic
// output.
//
// # Log level control
//
// The log level is controlled by the BREW_ENGINE_LOG_LEVEL environment
// variable. Valid values are: debug, info, warn, error. Default is "info".
//
//   - normal mode (info): key markers only — start/end, cache hits, errors.
//   - support mode (debug): full execution trace for troubleshooting.
//
// # Session correlation
//
// When BREW_ENGINE_SESSION_ID and/or BREW_ENGINE_REQUEST_ID are set, these
// values are injected as base fields into every log entry, enabling
// cross-layer correlation between UI and engine.
//
// # Log directory resolution (highest priority first)
//
//  1. The path set in the BREW_ENGINE_LOG_DIR environment variable (set by
//     internal/config from the application plist at startup).
//  2. BREW_TUI_LOG_DIR — legacy override, honoured for backward compatibility.
//  3. ~/.local/state/brew-engine/ — XDG Base Directory-compliant default.
//  4. /tmp/brew-engine/ — last-resort fallback when os.UserHomeDir fails.
//
// # Log rotation
//
// Log files are written to <LogDir>/daily/ with daily rotation and a 2 MB
// per-file size cap. Overflow files are named brew-engine-YYYY-MM-DD.N.log.
// At startup, daily files from previous months are automatically merged into
// <LogDir>/monthly/brew-engine-YYYY-MM.log and the originals are removed.
//
// # Log format
//
// Entries are written as structured JSON lines using Zap's production encoder
// with ISO 8601 timestamps. Every raw line captured from a brew subprocess is
// recorded here (ANSI-stripped), along with engine lifecycle events.
package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	// envLogDir is the primary env var for the log directory, set by
	// internal/config from the application plist at startup.
	envLogDir = "BREW_ENGINE_LOG_DIR"

	// envLogDirLegacy is the legacy env var honoured for backward compat.
	envLogDirLegacy = "BREW_TUI_LOG_DIR"

	// envLogLevel is the environment variable that controls log verbosity.
	// Valid values: debug, info, warn, error. Default: info.
	envLogLevel = "BREW_ENGINE_LOG_LEVEL"

	// envSessionID is the environment variable for session correlation.
	// When set, its value is included as a base field in every log entry.
	envSessionID = "BREW_ENGINE_SESSION_ID"

	// envRequestID is the environment variable for request correlation.
	// When set, its value is included as a base field in every log entry.
	envRequestID = "BREW_ENGINE_REQUEST_ID"

	// envBrewOutputLogFileName is the env var for the brew raw output log filename.
	envBrewOutputLogFileName = "BREW_ENGINE_BREW_OUTPUT_LOG_FILE_NAME"

	// defaultBrewOutputLogFileName is the fallback filename for the brew output log.
	defaultBrewOutputLogFileName = "brew-output.log"
)

// userHomeDir is the function used to locate the current user's home
// directory. It is a package-level variable so tests can override it to
// simulate a missing home directory and exercise the /tmp fallback path
// inside [resolveLogDir].
var userHomeDir = os.UserHomeDir

// Sugar is the package-level sugared logger. It supports both structured
// key-value pairs (Infow, Debugw, Warnw) and printf-style formatting
// (Infof, Debugf). Callers must guard against a nil Sugar before the
// first successful call to [Init] (the parser and cmd layer do this).
var Sugar *zap.SugaredLogger

// Raw is the package-level structured logger for high-performance,
// allocation-free key-value logging. Prefer [Sugar] for convenience
// unless logging is on a hot path.
var Raw *zap.Logger

// Level holds the current log level. Callers can check this before
// constructing expensive debug payloads:
//
//	if logger.Level == zapcore.DebugLevel {
//	    logger.Sugar.Debugw("expensive", "data", expensiveComputation())
//	}
var Level zapcore.Level = zapcore.InfoLevel

// Init initialises the global [Raw] and [Sugar] loggers. It must be called
// exactly once, before any subcommand runs (main does this). Calling Init
// more than once will open additional file descriptors for the log file
// without closing the previous ones.
//
// Init performs the following steps:
//  1. Resolve the log directory via [resolveLogDir].
//  2. Parse log level from BREW_ENGINE_LOG_LEVEL (default: info).
//  3. Merge any daily log files from previous months into monthly archives.
//  4. Open today's daily log file via [dailyWriter] (rotates at 2 MB).
//  5. Configure a Zap JSON encoder with ISO 8601 timestamps.
//  6. Inject session_id and request_id base fields if env vars are set.
//  7. Assign the built logger to the package-level [Raw] and [Sugar] vars.
//
// Returns a non-nil error if the log directory or daily file cannot be
// created. On error, [Raw] and [Sugar] remain nil and all log calls
// throughout the engine become no-ops.
func Init() error {
	logDir := resolveLogDir()
	Level = resolveLogLevel()

	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}

	// Merge previous months' daily files before opening today's file.
	MergeOldMonths(logDir)

	dailyDir := filepath.Join(logDir, dailySubDir)
	w, err := newDailyWriter(dailyDir)
	if err != nil {
		return err
	}

	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "ts"
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderCfg),
		zapcore.Lock(w),
		Level,
	)

	Raw = zap.New(core, zap.WithCaller(false))

	// Inject session/request correlation fields if present.
	baseFields := buildBaseFields()
	if len(baseFields) > 0 {
		Raw = Raw.With(baseFields...)
	}

	Sugar = Raw.Sugar()

	Sugar.Infow("brew-engine started",
		"log_dir", logDir,
		"log_level", Level.String(),
	)
	return nil
}

// Sync flushes any buffered log entries to disk. It should be called via
// defer immediately after a successful [Init] to ensure that the last
// entries written during process teardown are not lost.
//
// Sync is a no-op when [Raw] is nil (i.e. when Init was never called or
// failed). The error returned by the underlying zap.Logger.Sync is
// intentionally discarded because log-flush failures are non-actionable
// at shutdown time.
func Sync() {
	if Raw != nil {
		_ = Raw.Sync()
	}
}

// LogDir returns the resolved log directory path without creating it.
// It is safe to call at any time, including before [Init].
// The primary use case is allowing a subcommand to surface the log path
// in a JSON response so the frontend can offer a "Show Log" button.
func LogDir() string {
	return resolveLogDir()
}

// resolveLogDir determines the log directory using the priority order
// documented in the package comment. It never creates the directory;
// that responsibility belongs to [Init].
func resolveLogDir() string {
	if d := os.Getenv(envLogDir); d != "" {
		return d
	}
	if d := os.Getenv(envLogDirLegacy); d != "" {
		return d
	}
	home, err := userHomeDir()
	if err != nil {
		return filepath.Join("/tmp", "brew-engine")
	}
	return filepath.Join(home, ".local", "state", "brew-engine")
}

// resolveLogLevel parses BREW_ENGINE_LOG_LEVEL and returns the corresponding
// zapcore.Level. Unrecognised or empty values default to InfoLevel.
func resolveLogLevel() zapcore.Level {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(envLogLevel)))
	switch raw {
	case "debug":
		return zapcore.DebugLevel
	case "info", "":
		return zapcore.InfoLevel
	case "warn", "warning":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}

// buildBaseFields returns zap.Field entries for session and request IDs
// if the corresponding environment variables are set.
func buildBaseFields() []zap.Field {
	var fields []zap.Field
	if sid := os.Getenv(envSessionID); sid != "" {
		fields = append(fields, zap.String("session_id", sid))
	}
	if rid := os.Getenv(envRequestID); rid != "" {
		fields = append(fields, zap.String("request_id", rid))
	}
	return fields
}

// DebugEnabled returns true if the current log level allows debug output.
// Use this to guard expensive debug payload construction.
func DebugEnabled() bool {
	return Level <= zapcore.DebugLevel
}

// OpenBrewOutputLog opens (or creates) the brew-output.log file in the
// resolved log directory and writes a timestamped invocation header.
// The returned file must be closed by the caller when the brew subprocess
// exits.
//
// This log is separate from the structured Zap log: it captures the raw
// text output from every brew subprocess for post-mortem debugging.
// Format:
//
//	[2026-03-30T12:00:00Z] brew install humanlog
//	<raw stdout/stderr lines>
//	[EXIT 0]
//
// Returns nil when the file cannot be opened so callers can treat it as an
// optional writer without crashing on log failures.
func OpenBrewOutputLog(command string) *os.File {
	logDir := resolveLogDir()
	if logDir == "" {
		return nil
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil
	}

	name := os.Getenv(envBrewOutputLogFileName)
	if name == "" {
		name = defaultBrewOutputLogFileName
	}

	path := filepath.Join(logDir, name)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}

	header := fmt.Sprintf("\n[%s] %s\n", time.Now().UTC().Format(time.RFC3339), command)
	_, _ = f.WriteString(header)
	return f
}

// WriteBrewOutputFooter writes the exit-code footer to a brew output log file
// opened by [OpenBrewOutputLog]. It is a no-op when f is nil.
func WriteBrewOutputFooter(f *os.File, exitCode int) {
	if f == nil {
		return
	}
	_, _ = fmt.Fprintf(f, "[EXIT %d]\n", exitCode)
}
