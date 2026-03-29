// Package cmd This file implements the `brew-engine info` subcommand.
//
// The info command serves per-package metadata using a stale-while-revalidate
// cache strategy:
//
//   - Fresh cache hit  (< 24 h old): raw bytes from cache/info/<pkg>.json are
//     written directly to stdout with zero re-serialisation overhead.
//   - Stale cache hit  (≥ 24 h old): the cached bytes are emitted immediately
//     with [contract.Response.IsStale]=true, then a fresh fetch runs
//     synchronously and the up-to-date response follows on the same stdout
//     stream, allowing frontends to render quickly and then update.
//   - Cache miss or --force: `brew info --json=v2 <pkg>` runs, the result is
//     projected into [contract.FormulaInfo] or [contract.CaskInfo], cached,
//     and emitted.
//
// Precedence: if brew returns results in both buckets (a theoretical name
// collision), the formula takes precedence.
package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	"github.com/brewexplorer/brew-engine/internal/cache"
	"github.com/brewexplorer/brew-engine/internal/contract"
	"github.com/brewexplorer/brew-engine/internal/logger"
	"github.com/spf13/cobra"
)

// infoForceFlag is set by the --force / -f flag and causes the info command
// to bypass the cache, delete the stale entry, and fetch fresh data
// synchronously before re-caching.
var infoForceFlag bool

// execBrewInfo is the function used to run `brew info --json=v2 <pkg>`.
// It is a package-level variable so tests can inject a fake implementation
// without spawning a real brew process or manipulating PATH.
var execBrewInfo = func(pkg string) ([]byte, error) {
	return exec.Command("brew", "info", "--json=v2", pkg).Output()
}

// infoCmd is the Cobra command for `brew-engine info <package>`.
// It requires exactly one positional argument: the formula or cask name.
var infoCmd = &cobra.Command{
	Use:   "info <package>",
	Short: "Show detailed info for a formula or cask",
	Args:  cobra.ExactArgs(1),
	RunE:  runInfo,
}

func init() {
	rootCmd.AddCommand(infoCmd)
	infoCmd.Flags().BoolVarP(&infoForceFlag, "force", "f", false,
		"bypass the 24-hour TTL cache and fetch fresh data from brew")
}

// runInfo is the RunE handler for [infoCmd].
// It always returns nil so Cobra never generates additional output; all
// error reporting is done through [contract.WriteJSON].
func runInfo(_ *cobra.Command, args []string) error {
	pkg := args[0]

	// --force: evict any existing entry so ReadInfo returns ErrNotCached.
	if infoForceFlag {
		_ = cache.InvalidateInfo(pkg)
	}

	cached, isStale, readErr := cache.ReadInfo(pkg)

	switch {
	case readErr == nil && !isStale:
		// ── Fresh cache hit ──────────────────────────────────────────────────
		if logger.Sugar != nil {
			logger.Sugar.Debugw("info: fresh cache hit", "pkg", pkg)
		}
		_, _ = os.Stdout.Write(cached)
		return nil

	case readErr == nil && isStale:
		// ── Stale cache hit: emit immediately, then revalidate ───────────────
		if logger.Sugar != nil {
			logger.Sugar.Debugw("info: stale cache hit — revalidating", "pkg", pkg)
		}
		_, _ = os.Stdout.Write(injectIsStale(cached))

		// Revalidate: fetch fresh data and emit the updated response.
		// A WaitGroup keeps the process alive until the goroutine is done,
		// ensuring the second JSON line reaches stdout before exit.
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			fetchAndEmitInfo(pkg, os.Stdout)
		}()
		wg.Wait()
		return nil

	default:
		// ── Cache miss (or --force) ──────────────────────────────────────────
		fetchAndEmitInfo(pkg, os.Stdout)
		return nil
	}
}

// buildInfoResponse runs `brew info --json=v2 <pkg>`, projects the result
// into a [contract.FormulaInfo] or [contract.CaskInfo], and returns the
// marshalled [contract.Response] bytes (newline-terminated).
//
// The returned bytes are suitable for writing directly to stdout and for
// storing in the cache.
func buildInfoResponse(pkg string) ([]byte, error) {
	out, err := execBrewInfo(pkg)
	if err != nil {
		return nil, fmt.Errorf("brew info failed for %q: %w", pkg, err)
	}

	var raw contract.BrewInfoV2
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal brew output: %w", err)
	}

	var resp contract.Response

	if len(raw.Formulae) > 0 {
		f := raw.Formulae[0]
		fi := contract.FormulaInfo{
			Name:        f.Name,
			FullName:    f.FullName,
			Tap:         f.Tap,
			Description: f.Desc,
			Homepage:    f.Homepage,
			Version:     f.Versions.Stable,
			Installed:   len(f.Installed) > 0,
			Outdated:    f.Outdated,
			Pinned:      f.Pinned,
		}
		if len(f.Installed) > 0 {
			fi.InstalledVersion = f.Installed[0].Version
		}
		resp = contract.Response{Success: true, Type: "info", Data: fi}
	} else if len(raw.Casks) > 0 {
		c := raw.Casks[0]
		name := c.Token
		if len(c.Name) > 0 {
			name = c.Name[0]
		}
		resp = contract.Response{
			Success: true,
			Type:    "info",
			Data: contract.CaskInfo{
				Token:       c.Token,
				FullToken:   c.FullToken,
				Tap:         c.Tap,
				Name:        name,
				Description: c.Desc,
				Homepage:    c.Homepage,
				Version:     c.Version,
				Installed:   c.Installed != "",
				Outdated:    c.Outdated,
			},
		}
	} else {
		return nil, fmt.Errorf("package %q not found", pkg)
	}

	b, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// fetchAndEmitInfo fetches fresh package data via [buildInfoResponse], writes
// the result to the cache, and emits it to out. On error it emits a
// Type="error" response.
func fetchAndEmitInfo(pkg string, out io.Writer) {
	respBytes, err := buildInfoResponse(pkg)
	if err != nil {
		contract.WriteJSON(out, contract.Response{
			Success: false,
			Type:    "error",
			Error:   err.Error(),
		})
		return
	}

	_ = cache.WriteInfo(pkg, respBytes)

	if logger.Sugar != nil {
		logger.Sugar.Debugw("info: fetched and cached", "pkg", pkg)
	}

	_, _ = out.Write(respBytes)
}

// injectIsStale clones a cached [contract.Response] JSON line and sets
// IsStale=true on the envelope, returning the modified newline-terminated
// bytes. If the input cannot be round-tripped (malformed cache), the original
// bytes are returned unchanged.
func injectIsStale(data []byte) []byte {
	trimmed := bytes.TrimRight(data, "\n")
	var r contract.Response
	if err := json.Unmarshal(trimmed, &r); err != nil {
		return data
	}
	r.IsStale = true
	b, err := json.Marshal(r)
	if err != nil {
		return data
	}
	return append(b, '\n')
}
