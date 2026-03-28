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
// # Log directory resolution (highest priority first)
//
//  1. The path set in the BREW_TUI_LOG_DIR environment variable.
//  2. ~/.local/state/brew-engine/ — XDG Base Directory-compliant default.
//  3. /tmp/brew-engine/ — last-resort fallback when os.UserHomeDir fails.
//
// # Log format
//
// Entries are written as structured JSON lines using Zap's production encoder
// with ISO 8601 timestamps. Every raw line captured from a brew subprocess is
// recorded here (ANSI-stripped), along with engine lifecycle events.
package logger

import (
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	// envLogDir is the environment variable that overrides the default log
	// directory. When set, the value is used verbatim with no further
	// expansion (no tilde expansion, no shell substitutions).
	envLogDir = "BREW_TUI_LOG_DIR"

	// logFileName is the name of the append-only log file created inside
	// the resolved log directory.
	logFileName = "brew-engine.log"
)

// Sugar is the package-level sugared logger. It supports both structured
// key-value pairs (Infow, Debugw, Warnw) and printf-style formatting
// (Infof, Debugf). Callers must guard against a nil Sugar before the
// first successful call to [Init] (the parser and cmd layer do this).
var Sugar *zap.SugaredLogger

// Raw is the package-level structured logger for high-performance,
// allocation-free key-value logging. Prefer [Sugar] for convenience
// unless logging is on a hot path.
var Raw *zap.Logger

// Init initialises the global [Raw] and [Sugar] loggers. It must be called
// exactly once, before any subcommand runs (main does this). Calling Init
// more than once will open additional file descriptors for the log file
// without closing the previous ones.
//
// Init performs the following steps:
//  1. Resolve the log directory via [resolveLogDir].
//  2. Create the directory tree (0755) if it does not exist.
//  3. Open (or create) the log file in append mode (0644).
//  4. Configure a Zap JSON encoder with ISO 8601 timestamps.
//  5. Assign the built logger to the package-level [Raw] and [Sugar] vars.
//
// Returns a non-nil error if the directory or file cannot be created, or
// if the file cannot be opened for writing. On error, [Raw] and [Sugar]
// remain nil and all log calls throughout the engine become no-ops.
func Init() error {
	logDir := resolveLogDir()

	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}

	logPath := filepath.Join(logDir, logFileName)
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}

	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "ts"
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderCfg),
		zapcore.AddSync(f),
		zapcore.DebugLevel,
	)

	Raw = zap.New(core, zap.WithCaller(false))
	Sugar = Raw.Sugar()

	Sugar.Infow("brew-engine started", "log_dir", logDir)
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
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("/tmp", "brew-engine")
	}
	return filepath.Join(home, ".local", "state", "brew-engine")
}
