package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// maxLogFileSize is the maximum size of a single daily log file.
// When a write would exceed this limit, a new file with the next
// numeric suffix is opened before writing.
const maxLogFileSize = 2 * 1024 * 1024 // 2 MB

// dailySubDir is the subdirectory under LogDir that holds per-day log files.
const dailySubDir = "daily"

// monthlySubDir is the subdirectory under LogDir that holds merged monthly logs.
const monthlySubDir = "monthly"

// dailyFilePattern matches brew-engine-YYYY-MM-DD[.N].log
// Capture groups: (1) date YYYY-MM-DD, (2) optional suffix index
var dailyFilePattern = regexp.MustCompile(`^brew-engine-(\d{4}-\d{2}-\d{2})(?:\.(\d+))?\.log$`)

// ─── dailyWriter ──────────────────────────────────────────────────────────────

// dailyWriter is a zapcore.WriteSyncer that writes to date-stamped log files
// and rotates on day change or when the 2 MB per-file limit is reached.
//
// File naming convention inside <LogDir>/daily/:
//
//	brew-engine-2026-03-30.log     — primary file for that day (suffix -1)
//	brew-engine-2026-03-30.0.log   — overflow #0 (> 2 MB)
//	brew-engine-2026-03-30.1.log   — overflow #1, etc.
type dailyWriter struct {
	mu            sync.Mutex
	dir           string // absolute path to the daily/ subdirectory
	currentFile   *os.File
	currentDate   string // "2006-01-02"
	currentSize   int64
	currentSuffix int // -1 = base file, 0+ = overflow index
}

// newDailyWriter creates a dailyWriter rooted at dir (the daily/ subdirectory).
// It opens the correct log file for today, continuing an existing file if it
// is below maxLogFileSize, or creating a new suffix file if it is full.
func newDailyWriter(dir string) (*dailyWriter, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("logger: cannot create daily log dir %s: %w", dir, err)
	}

	today := currentDate()
	f, size, suffix, err := openLogFileForDate(dir, today)
	if err != nil {
		return nil, err
	}

	return &dailyWriter{
		dir:           dir,
		currentFile:   f,
		currentDate:   today,
		currentSize:   size,
		currentSuffix: suffix,
	}, nil
}

// Write implements io.Writer / zapcore.WriteSyncer.
// It is safe for concurrent use.
func (w *dailyWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	today := currentDate()

	// Day changed → probe for the right file for the new date.
	if today != w.currentDate {
		if err := w.rotateToDate(today); err != nil {
			return 0, err
		}
	}

	// Size limit exceeded → always advance to the next numbered overflow file.
	if w.currentSize+int64(len(p)) > maxLogFileSize {
		if err := w.rotateToNextSuffix(); err != nil {
			return 0, err
		}
	}

	n, err := w.currentFile.Write(p)
	w.currentSize += int64(n)
	return n, err
}

// Sync implements zapcore.WriteSyncer.
func (w *dailyWriter) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.currentFile != nil {
		return w.currentFile.Sync()
	}
	return nil
}

// rotateToDate handles a calendar-day rollover.
// It probes for the highest existing suffix file for the new date and opens it
// (or creates the base file if none exist). Must be called with w.mu held.
func (w *dailyWriter) rotateToDate(date string) error {
	if w.currentFile != nil {
		_ = w.currentFile.Sync()
		_ = w.currentFile.Close()
	}
	f, size, suffix, err := openLogFileForDate(w.dir, date)
	if err != nil {
		return err
	}
	w.currentFile = f
	w.currentDate = date
	w.currentSize = size
	w.currentSuffix = suffix
	return nil
}

// rotateToNextSuffix handles a per-file size limit being reached.
// It always creates a brand-new file with the next numeric suffix,
// regardless of the current file's size on disk. Must be called with w.mu held.
func (w *dailyWriter) rotateToNextSuffix() error {
	if w.currentFile != nil {
		_ = w.currentFile.Sync()
		_ = w.currentFile.Close()
	}
	newSuffix := w.currentSuffix + 1
	path := logFilePath(w.dir, w.currentDate, newSuffix)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("logger: cannot open overflow log file %s: %w", path, err)
	}
	w.currentFile = f
	w.currentSuffix = newSuffix
	w.currentSize = 0
	return nil
}

// ─── File resolution ──────────────────────────────────────────────────────────

// openLogFileForDate finds or creates the correct log file for date inside dir.
// It picks the latest existing file for that date. If that file is at or above
// maxLogFileSize it creates a new overflow file with the next numeric suffix.
// Returns the open file (append mode), its current byte size, and the suffix used.
func openLogFileForDate(dir, date string) (*os.File, int64, int, error) {
	path, suffix := resolveLogFilePath(dir, date)

	// If the resolved file already exists and is full, bump the suffix.
	if info, err := os.Stat(path); err == nil && info.Size() >= maxLogFileSize {
		suffix++
		path = logFilePath(dir, date, suffix)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("logger: cannot open log file %s: %w", path, err)
	}

	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, 0, err
	}
	return f, info.Size(), suffix, nil
}

// resolveLogFilePath returns the path and numeric suffix for the latest log
// file matching date in dir. If no files exist for that date, suffix is -1
// and the returned path uses no suffix (i.e. brew-engine-YYYY-MM-DD.log).
func resolveLogFilePath(dir, date string) (path string, suffix int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return logFilePath(dir, date, -1), -1
	}

	maxSuffix := -2 // -2 means "nothing found yet"
	for _, e := range entries {
		m := dailyFilePattern.FindStringSubmatch(e.Name())
		if m == nil || m[1] != date {
			continue
		}
		if m[2] == "" {
			// Base file (no suffix) → treat as suffix -1
			if maxSuffix < -1 {
				maxSuffix = -1
			}
		} else {
			var n int
			fmt.Sscanf(m[2], "%d", &n)
			if n > maxSuffix {
				maxSuffix = n
			}
		}
	}

	if maxSuffix == -2 {
		// No file found for this date yet.
		return logFilePath(dir, date, -1), -1
	}
	return logFilePath(dir, date, maxSuffix), maxSuffix
}

// logFilePath builds the full path for a daily log file.
// suffix -1 → brew-engine-YYYY-MM-DD.log
// suffix  0 → brew-engine-YYYY-MM-DD.0.log
// suffix  N → brew-engine-YYYY-MM-DD.N.log
func logFilePath(dir, date string, suffix int) string {
	if suffix < 0 {
		return filepath.Join(dir, fmt.Sprintf("brew-engine-%s.log", date))
	}
	return filepath.Join(dir, fmt.Sprintf("brew-engine-%s.%d.log", date, suffix))
}

// currentDate returns today's date formatted as "2006-01-02".
var currentDate = func() string {
	return time.Now().Format("2006-01-02")
}

// ─── Monthly merge ────────────────────────────────────────────────────────────

// MergeOldMonths scans the daily/ subdirectory under logDir and merges all
// daily log files that belong to any month other than the current month into
// a single monthly archive at <logDir>/monthly/brew-engine-YYYY-MM.log.
// Daily files are deleted after a successful merge.
//
// This is called once at logger startup. Errors are non-fatal: a failed merge
// is silently skipped so that the engine still starts successfully.
func MergeOldMonths(logDir string) {
	dailyDir := filepath.Join(logDir, dailySubDir)
	monthlyDir := filepath.Join(logDir, monthlySubDir)

	entries, err := os.ReadDir(dailyDir)
	if err != nil {
		return
	}

	currentMonth := time.Now().Format("2006-01")

	// Group files by month (YYYY-MM).
	byMonth := make(map[string][]string)
	for _, e := range entries {
		m := dailyFilePattern.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		month := m[1][:7] // "YYYY-MM"
		if month == currentMonth {
			continue
		}
		byMonth[month] = append(byMonth[month], filepath.Join(dailyDir, e.Name()))
	}

	if len(byMonth) == 0 {
		return
	}

	if err := os.MkdirAll(monthlyDir, 0o755); err != nil {
		return
	}

	for month, files := range byMonth {
		sort.Strings(files) // chronological order
		dest := filepath.Join(monthlyDir, fmt.Sprintf("brew-engine-%s.log", month))
		if mergeFiles(dest, files) == nil {
			for _, f := range files {
				_ = os.Remove(f)
			}
		}
	}
}

// mergeFiles appends the contents of each src file to dest (append mode).
// Returns the first error encountered, if any.
func mergeFiles(dest string, srcs []string) error {
	out, err := os.OpenFile(dest, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()

	for _, src := range srcs {
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		_ = in.Close()
		if copyErr != nil {
			return copyErr
		}

		// Separator between merged files for readability.
		if !strings.HasSuffix(src, srcs[len(srcs)-1]) {
			_, _ = out.WriteString("\n")
		}
	}
	return nil
}
