// Package logger configures the global Zap logger that writes to a background
// log file. stdout is intentionally excluded — it is reserved for JSON payloads.
//
// Log directory resolution (highest priority first):
//  1. $BREW_TUI_LOG_DIR environment variable
//  2. ~/.local/state/brew-engine/  (XDG-compliant default)
package logger

import (
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	envLogDir   = "BREW_TUI_LOG_DIR"
	logFileName = "brew-engine.log"
)

// Sugar is the package-level sugared logger for structured, printf-style logging.
var Sugar *zap.SugaredLogger

// Raw is the package-level logger for structured key-value logging.
var Raw *zap.Logger

// Init initialises the global logger. Must be called exactly once, before any
// subcommand runs. Returns an error if the log directory or file cannot be created.
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

// Sync flushes any buffered log entries. Call via defer in main().
func Sync() {
	if Raw != nil {
		_ = Raw.Sync()
	}
}

// LogDir returns the resolved log directory path. Safe to call after Init().
func LogDir() string {
	return resolveLogDir()
}

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
