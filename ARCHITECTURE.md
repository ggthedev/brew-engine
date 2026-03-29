# brew-engine — Architecture Reference

This document describes the internal structure of brew-engine: its layers,
the boundaries between them, every public and private interface, and a
step-by-step guide to adding a new subcommand.

---

## Table of Contents

1. [Layered Overview](#1-layered-overview)
2. [Layer Responsibilities](#2-layer-responsibilities)
   - 2.1 [Entry point — `main`](#21-entry-point--main)
   - 2.2 [Command layer — `cmd/`](#22-command-layer--cmd)
   - 2.3 [Cache layer — `internal/cache`](#23-cache-layer--internalcache)
   - 2.4 [Contract layer — `internal/contract`](#24-contract-layer--internalcontract)
   - 2.5 [Parser layer — `internal/parser`](#25-parser-layer--internalparser)
   - 2.6 [Logger layer — `internal/logger`](#26-logger-layer--internallogger)
3. [Public Interfaces](#3-public-interfaces)
4. [Private / Injectable Interfaces](#4-private--injectable-interfaces)
5. [The JSON stdout Contract](#5-the-json-stdout-contract)
6. [The Caching Layer — End-to-End Flow](#6-the-caching-layer--end-to-end-flow)
7. [Adding a New Subcommand](#7-adding-a-new-subcommand)

---

## 1. Layered Overview

```
┌──────────────────────────────────────────────────────────┐
│                      Frontend (UI)                       │
│          SwiftUI app            Bubble Tea TUI           │
│       (subprocess via Process)  (direct Go import)       │
└───────────────────┬──────────────────────┬───────────────┘
                    │ stdout (JSON lines)  │ direct import
                    ▼                      ▼
┌──────────────────────────────────────────────────────────┐
│                     main.go                              │
│   logger.Init() ──► cmd.Execute()                        │
└──────────────────────────────────────────────────────────┘
                    │
                    ▼
┌──────────────────────────────────────────────────────────┐
│                     cmd/  (Cobra)                        │
│  root.go   list.go   info.go   install.go   remove.go    │
│                      watch.go                            │
└──────┬──────────────────────────────────────────┬────────┘
       │ uses                                     │ uses
       ▼                                          ▼
┌─────────────────────┐              ┌─────────────────────┐
│  internal/cache     │              │  internal/parser    │
│  cache.go           │              │  parser.go          │
│  watcher.go         │              └─────────────────────┘
└─────────────────────┘                         │
       │ uses                                   │ both use
       ▼                                        ▼
┌─────────────────────┐              ┌─────────────────────┐
│  internal/contract  │              │  internal/logger    │
│  types.go           │              │  logger.go          │
└─────────────────────┘              └─────────────────────┘
                    │
                    ▼
              exec.Command("brew", ...)
```

**Invariant: `os.Stdout` receives only complete, newline-terminated
`contract.Response` JSON objects. Nothing else may be written to stdout
anywhere in the codebase.**

---

## 2. Layer Responsibilities

### 2.1 Entry point — `main`

`main.go` is intentionally minimal:

```
logger.Init()   // opens the log file; sets logger.Sugar and logger.Raw
defer logger.Sync()
cmd.Execute()   // hands control to Cobra; never returns on success
```

It owns no business logic. It exists purely to sequence initialization
before the Cobra tree takes over.

---

### 2.2 Command layer — `cmd/`

Each file in `cmd/` registers one Cobra subcommand via its `init()` function.
Every `RunE` handler **must**:

- Write all output through `contract.WriteJSON(os.Stdout, …)`.
- Return `nil` (not an error), so Cobra never emits its own formatted messages.
- Emit a `Type="error"` response if the brew operation fails.

| File | Subcommand | Strategy |
|---|---|---|
| `root.go` | (root) | Emits JSON error on invocation with no subcommand; sets `SilenceErrors`/`SilenceUsage` |
| `list.go` | `list` | Cache-first (infinite TTL); falls back to `cache.BuildAndCacheList` |
| `info.go` | `info <pkg>` | Stale-while-revalidate (24h TTL); `--force` / `-f` bypasses cache |
| `install.go` | `install <pkg>` | Delegates directly to `parser.RunInstall`; streams progress events |
| `remove.go` | `remove <pkg>` | Delegates directly to `parser.RunRemove`; streams progress events |
| `watch.go` | `watch` | Starts `cache.StartWatcher`; blocks on `signal.NotifyContext` until SIGINT/SIGTERM |

#### The `Execute` safety net

`cmd.Execute` (called by `main`) wraps Cobra's own `Execute()`. If Cobra
returns an error (e.g. unrecognised subcommand, wrong argument count) after
all `RunE` handlers have already returned `nil`, `Execute` converts it to a
JSON error line and calls `os.Exit(1)`, preserving the stdout invariant at
the framework boundary.

---

### 2.3 Cache layer — `internal/cache`

Two files, two concerns:

| File | Concern |
|---|---|
| `cache.go` | Read/write/invalidate flat JSON files; `BuildAndCacheList` orchestration |
| `watcher.go` | `fsnotify` watcher lifecycle, debounce loop, background list rebuild |

**Cache directory resolution** (`CacheDir`):

1. `BREW_TUI_CACHE_DIR` env var (verbatim, no tilde expansion).
2. `~/.local/state/brew-engine/cache/` (XDG-compliant default).
3. `/tmp/brew-engine/cache/` (fallback when `os.UserHomeDir` fails).

**Namespace layout on disk:**

```
$BREW_TUI_CACHE_DIR/
├── list.json          # monolithic NamesList snapshot; infinite TTL
└── info/
    ├── wget.json      # per-package Response snapshot; 24h TTL
    └── jq.json
```

Every file stores a **complete, newline-terminated `contract.Response` JSON
line** — the same bytes that would be written to stdout — so a cache hit
requires only a single `os.ReadFile` + `os.Stdout.Write` with no
re-serialisation.

---

### 2.4 Contract layer — `internal/contract`

The single source of truth for the wire format. Divided into four groups:

| Group | Types | Visibility |
|---|---|---|
| **Envelope** | `Response`, `WriteJSON` | Public; all output passes through `WriteJSON` |
| **UI types** | `FormulaInfo`, `CaskInfo`, `ListData`, `NamesList`, `CacheEvent` | Public; these are the shapes the frontend decodes |
| **Raw types** | `BrewInfoV2`, `RawFormula`, `RawCask`, `RawVersions`, `RawInstalled` | Public (exported for tests) but **never serialised to stdout**; internal unmarshal targets only |
| **Streaming types** | `ProgressStep`, `DoneData` | Public; payloads for install/remove event streams |

`WriteJSON` is the **only** sanctioned way to produce output. It marshals a
`Response`, appends `\n`, and writes atomically. If marshalling fails (should
never happen given the concrete types), it writes a hard-coded fallback JSON
error so stdout stays valid.

---

### 2.5 Parser layer — `internal/parser`

Handles the long-running `brew install` / `brew remove` processes:

- Attaches separate goroutines to `stdout` and `stderr` pipes.
- Strips ANSI escape sequences from every line.
- Emits a `Type="progress"` JSON line for each `==>` header.
- Emits `Type="done"` or `Type="error"` as the terminal event.

The two public entry points are `RunInstall(pkg, out)` and
`RunRemove(pkg, out)`. Both delegate to the private
`runStreamingCommand(pkg, brewArgs, out)`.

`execBrewCommand` (a package-level `var`) is the `exec.Cmd` factory;
tests override it to inject fake binaries without touching `PATH`.

---

### 2.6 Logger layer — `internal/logger`

Wraps `go.uber.org/zap`. Provides two package-level singletons:

| Var | Type | Use |
|---|---|---|
| `logger.Sugar` | `*zap.SugaredLogger` | Structured key-value logging in commands and cache |
| `logger.Raw` | `*zap.Logger` | Raw structured logging in parser (not via sugar) |

Log directory resolution (`resolveLogDir`) follows the same three-tier
priority as `CacheDir`:

1. `BREW_TUI_LOG_DIR` env var.
2. `~/.local/state/brew-engine/`.
3. `/tmp/brew-engine/` (fallback).

`logger.Sugar` is `nil` until `logger.Init()` is called. All callers
guard: `if logger.Sugar != nil { … }` — the logger is always optional,
never required for correct operation.

---

## 3. Public Interfaces

These are the exported symbols that cross package boundaries (other packages
or the future Go TUI can import and use them directly):

### `internal/contract`

```go
// Envelope
type Response struct { Success bool; Type string; IsStale bool; Error string; Data interface{} }
func WriteJSON(w io.Writer, r Response)

// UI types (Data payloads sent to frontends)
type FormulaInfo struct { ... }
type CaskInfo     struct { ... }
type ListData     struct { Formulae []FormulaInfo; Casks []CaskInfo; Total int }
type NamesList    struct { Formulae []string; Casks []string; Total int }
type CacheEvent   struct { Action string; Target string }
type ProgressStep struct { Package string; Step string }
type DoneData     struct { Package string; ExitCode int }
```

### `internal/cache`

```go
// Errors
var ErrNotCached error

// Directory helpers
func CacheDir() string
func ListCachePath() string
func InfoCachePath(pkg string) string

// list.json operations
func WriteList(data []byte) error
func ReadList() ([]byte, error)          // returns ErrNotCached on miss
func InvalidateList() error

// info/<pkg>.json operations
func WriteInfo(pkg string, data []byte) error
func ReadInfo(pkg string) (data []byte, isStale bool, err error)
func InvalidateInfo(pkg string) error

// Orchestration
func BuildAndCacheList() ([]byte, error)

// Watcher
var BrewWatchDirs []string               // overridable in tests
func StartWatcher(ctx context.Context, out io.Writer) error
```

### `internal/logger`

```go
var Sugar *zap.SugaredLogger
var Raw   *zap.Logger
func Init() error
func Sync()
func LogDir() string
```

### `internal/parser`

```go
func RunInstall(pkg string, out io.Writer)
func RunRemove(pkg string, out io.Writer)
```

### `cmd`

```go
func Execute()   // sole entry point called by main
```

---

## 4. Private / Injectable Interfaces

These are unexported symbols that are exposed as `var` function variables
specifically to enable test injection without modifying `PATH` or the
filesystem:

| Variable | Package | Default | Overridden in tests to… |
|---|---|---|---|
| `execCommand` | `internal/cache` | `exec.Command` | Inject a fake `brew` binary |
| `userHomeDir` | `internal/cache` | `os.UserHomeDir` | Simulate a missing home directory |
| `newFSWatcher` | `internal/cache` | `fsnotify.NewWatcher` | Simulate watcher creation failure |
| `execBrewCommand` | `internal/parser` | `exec.Command` | Inject a fake `brew` binary |
| `userHomeDir` | `internal/logger` | `os.UserHomeDir` | Simulate a missing home directory |

The pattern is consistent across all packages:

```go
// production default
var execCommand = func(name string, args ...string) *exec.Cmd {
    return exec.Command(name, args...)
}

// test override
orig := execCommand
t.Cleanup(func() { execCommand = orig })
execCommand = fakeBrew(t, "#!/bin/sh\nprintf 'wget jq'\nexit 0\n")
```

Private helper functions (`runWatcher`, `rebuildListCache`, `fetchNames`,
`buildInfoResponse`, `injectIsStale`, `runStreamingCommand`, `stripANSI`)
are unexported and tested either directly (same-package `_test.go`) or via
their exported callers.

---

## 5. The JSON stdout Contract

Every response — success, error, progress, or cache event — is an instance
of `contract.Response` serialised as a **single, complete, newline-terminated
JSON object**.

```
{"success":true|false,"type":"<type>","is_stale":true,"error":"<msg>","data":{…}}\n
```

### Type taxonomy

| `type` | `data` shape | Emitted by |
|---|---|---|
| `"list"` | `NamesList` | `cmd/list.go` |
| `"info"` | `FormulaInfo` or `CaskInfo` | `cmd/info.go` |
| `"progress"` | `ProgressStep` | `internal/parser` (during install/remove) |
| `"done"` | `DoneData` | `internal/parser` (on exit 0) |
| `"error"` | `DoneData` (optional) | any layer on failure |
| `"event"` | `CacheEvent` | `internal/cache` watcher on rebuild |

### `is_stale` flag

Only present on `"info"` responses served from an expired cache entry.
When `true`, the frontend should render the current data immediately and
wait for the second JSON line (the fresh response) that follows on the
same stdout stream.

### `"event"` stream

`brew-engine watch` emits `"event"` lines asynchronously whenever the
background watcher detects an external Homebrew mutation:

```json
{"success":true,"type":"event","data":{"action":"cache_rebuilt","target":"list"}}
```

The frontend should treat this as a signal to re-invoke `brew-engine list`.

---

## 6. The Caching Layer — End-to-End Flow

### `list` — infinite TTL, watcher-invalidated

```
brew-engine list
     │
     ├─ cache.ReadList()
     │      ├─ hit  ──► write raw bytes to stdout  (zero re-serialisation)
     │      └─ miss ──► cache.BuildAndCacheList()
     │                     ├─ exec "brew list --formula"
     │                     ├─ exec "brew list --cask"
     │                     ├─ marshal → contract.Response{Type:"list", Data:NamesList}
     │                     ├─ cache.WriteList(bytes)   ← best-effort
     │                     └─ write bytes to stdout

brew-engine watch  (long-running)
     │
     └─ cache.StartWatcher(ctx, stdout)
             │
             ├─ fsnotify.Watcher on BrewWatchDirs
             └─ goroutine: runWatcher(ctx, events, errs, close, stdout)
                     │
                     └─ on event: debounce 500ms, then rebuildListCache(stdout)
                             ├─ cache.InvalidateList()
                             ├─ cache.BuildAndCacheList()
                             └─ contract.WriteJSON → {"type":"event","data":{"action":"cache_rebuilt"}}
```

### `info` — 24h TTL, stale-while-revalidate

```
brew-engine info wget
     │
     ├─ --force flag?  ──► cache.InvalidateInfo("wget")
     │
     ├─ cache.ReadInfo("wget")
     │      ├─ fresh (< 24h)  ──► write raw bytes to stdout
     │      ├─ stale (≥ 24h)  ──► write bytes with is_stale:true
     │      │                     goroutine: fetchAndEmitInfo  ──► write fresh bytes
     │      └─ miss / force   ──► fetchAndEmitInfo
     │                               ├─ exec "brew info --json=v2 wget"
     │                               ├─ json.Unmarshal → contract.BrewInfoV2
     │                               ├─ project → FormulaInfo or CaskInfo
     │                               ├─ marshal → contract.Response{Type:"info"}
     │                               ├─ cache.WriteInfo("wget", bytes)
     │                               └─ write bytes to stdout
```

---

## 7. Adding a New Subcommand

This is the complete checklist for adding, say, `brew-engine upgrade <pkg>`.

### Step 1 — Define the contract types (if needed)

If the new command introduces a new response shape, add it to
`internal/contract/types.go`:

```go
// UpgradeResult is the Data payload for a Type="upgrade" response.
type UpgradeResult struct {
    Package    string `json:"package"`
    OldVersion string `json:"old_version"`
    NewVersion string `json:"new_version"`
}
```

Add the new `type` value to the `Response.Type` field comment.

### Step 2 — Create `cmd/upgrade.go`

```go
package cmd

import (
    "os"

    "github.com/brewexplorer/brew-engine/internal/contract"
    "github.com/spf13/cobra"
)

var upgradeCmd = &cobra.Command{
    Use:   "upgrade <package>",
    Short: "Upgrade a formula or cask to its latest version",
    Args:  cobra.ExactArgs(1),
    RunE:  runUpgrade,
}

func init() {
    rootCmd.AddCommand(upgradeCmd)
}

func runUpgrade(_ *cobra.Command, args []string) error {
    pkg := args[0]

    // … business logic …

    contract.WriteJSON(os.Stdout, contract.Response{
        Success: true,
        Type:    "upgrade",
        Data:    contract.UpgradeResult{ /* … */ },
    })
    return nil   // always nil — Cobra must not produce its own output
}
```

**Rules to follow (enforced by the stdout invariant):**

- Every output path — success, partial failure, fatal error — calls
  `contract.WriteJSON`. Never `fmt.Print`, `log.Print`, or `os.Stderr.Write`.
- Return `nil` from `RunE`. If an error must surface, encode it in a
  `Type="error"` response before returning `nil`.
- Never call `os.Exit` directly; only `cmd.Execute` may call it.

### Step 3 — If the command streams output, use `internal/parser`

For commands that invoke a long-running `brew` process and need to stream
`==>` progress lines, delegate to `parser.runStreamingCommand` (or expose a
new `RunXxx` wrapper following the pattern of `RunInstall`/`RunRemove`):

```go
// internal/parser/parser.go
func RunUpgrade(pkg string, out io.Writer) {
    runStreamingCommand(pkg, []string{"upgrade", pkg}, out)
}
```

### Step 4 — If the command needs caching, use `internal/cache`

| Cache need | Function to use |
|---|---|
| Serve previously-built list data | `cache.ReadList` / `cache.WriteList` |
| Serve per-package data with a TTL | `cache.ReadInfo` / `cache.WriteInfo` / `cache.InvalidateInfo` |
| Evict and rebuild the list | `cache.InvalidateList` + `cache.BuildAndCacheList` |

### Step 5 — Add injectable `execCommand` if the command shells out

If the command calls an external binary, expose a testable factory var:

```go
// cmd/upgrade.go
var execUpgradeCommand = func(args ...string) *exec.Cmd {
    return exec.Command("brew", args...)
}
```

Override it in tests the same way the existing commands do with `fakeBrew`.

### Step 6 — Write tests in `cmd/upgrade_test.go`

Minimum coverage targets (matching the existing suite):

| Scenario | What to test |
|---|---|
| Success | Correct JSON type and data fields on stdout |
| `brew` exits non-zero | `Type="error"` response emitted; `RunE` returns `nil` |
| Cache hit (if applicable) | Bytes served directly; `brew` not invoked |
| Cache miss (if applicable) | `brew` invoked; cache file written |
| `--force` flag (if applicable) | Cache evicted before fetch |

Use `t.Setenv("BREW_TUI_CACHE_DIR", t.TempDir())` to isolate every test
from the real cache. Use `t.Setenv("PATH", t.TempDir())` to guard against
accidental real `brew` invocations on cache-hit paths.

### Step 7 — Update `cmd/root.go` doc comment and `README.md`

Add the new subcommand to:

- The `// # Subcommands` godoc block in `cmd/root.go`.
- The `Type taxonomy` table in `README.md` under **JSON Communication Contract**.
- The **Build & Run** usage examples in `README.md`.
