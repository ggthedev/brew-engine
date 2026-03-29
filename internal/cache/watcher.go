package cache

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/brewexplorer/brew-engine/internal/contract"
	"github.com/brewexplorer/brew-engine/internal/logger"
	"github.com/fsnotify/fsnotify"
)

// BrewWatchDirs is the set of Homebrew directories monitored for filesystem
// changes. It is an exported variable so tests (including external packages
// such as cmd) can substitute lightweight temporary directories without
// requiring a real Homebrew installation.
var BrewWatchDirs = []string{
	"/opt/homebrew/Cellar",
	"/opt/homebrew/Caskroom",
	"/opt/homebrew/var/homebrew/locks",
}

// debounceInterval is the quiet period after the last filesystem event before
// a cache rebuild is triggered. Coalescing rapid bursts (e.g. a single
// `brew install` touching dozens of files) into one rebuild keeps stdout clean.
const debounceInterval = 500 * time.Millisecond

// newFSWatcher is an injectable factory for creating a new fsnotify watcher.
// Tests can substitute it to simulate fsnotify.NewWatcher failures.
var newFSWatcher = func() (*fsnotify.Watcher, error) { return fsnotify.NewWatcher() }

// StartWatcher registers fsnotify watches on [brewWatchDirs], then launches
// a background goroutine that debounces events and calls [rebuildListCache]
// whenever Homebrew mutates the monitored directories.
//
// The goroutine runs until ctx is cancelled, at which point the fsnotify
// watcher is closed and the goroutine exits cleanly.
//
// cache rebuild events are written as [contract.Response] JSON lines to out
// (typically os.Stdout in the watch subcommand).
//
// Returns an error only when no watch directory could be added — for example,
// on a machine without Homebrew installed. Partial success (some dirs missing)
// is silently tolerated and logged at WARN level.
func StartWatcher(ctx context.Context, out io.Writer) error {
	w, err := newFSWatcher()
	if err != nil {
		return err
	}

	added := 0
	for _, dir := range BrewWatchDirs {
		if err := w.Add(dir); err != nil {
			if logger.Sugar != nil {
				logger.Sugar.Warnw("watcher: skipping directory", "dir", dir, "err", err)
			}
			continue
		}
		added++
	}

	if added == 0 {
		w.Close()
		return errors.New("watcher: no Homebrew directories available to watch")
	}

	go runWatcher(ctx, w.Events, w.Errors, w.Close, out)
	return nil
}

// runWatcher is the main event loop. It debounces filesystem events and
// triggers a cache rebuild after each quiet period. It exits when ctx is
// cancelled or the supplied channels are closed.
//
// Accepting raw channels (rather than *fsnotify.Watcher) makes this function
// independently testable without OS-level filesystem events.
func runWatcher(
	ctx context.Context,
	events <-chan fsnotify.Event,
	errs <-chan error,
	close func() error,
	out io.Writer,
) {
	defer close() //nolint:errcheck

	var timer *time.Timer

	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return

		case event, ok := <-events:
			if !ok {
				return
			}
			if logger.Sugar != nil {
				logger.Sugar.Debugw("watcher: fs event", "op", event.Op.String(), "path", event.Name)
			}
			// Reset the debounce timer on every new event.
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(debounceInterval, func() {
				rebuildListCache(out)
			})

		case err, ok := <-errs:
			if !ok {
				return
			}
			if logger.Sugar != nil {
				logger.Sugar.Warnw("watcher: error", "err", err)
			}
		}
	}
}

// rebuildListCache invalidates list.json, runs a fresh `brew list` to rebuild
// it via [BuildAndCacheList], and emits a Type="event" [contract.Response] to
// out so that the frontend knows to refresh its installed-packages view.
func rebuildListCache(out io.Writer) {
	if err := InvalidateList(); err != nil {
		if logger.Sugar != nil {
			logger.Sugar.Errorw("watcher: failed to invalidate list.json", "err", err)
		}
		return
	}

	if _, err := BuildAndCacheList(); err != nil {
		if logger.Sugar != nil {
			logger.Sugar.Warnw("watcher: list rebuild failed", "err", err)
		}
		return
	}

	contract.WriteJSON(out, contract.Response{
		Success: true,
		Type:    "event",
		Data:    contract.CacheEvent{Action: "cache_rebuilt", Target: "list"},
	})

	if logger.Sugar != nil {
		logger.Sugar.Infow("watcher: list cache rebuilt and event emitted")
	}
}
