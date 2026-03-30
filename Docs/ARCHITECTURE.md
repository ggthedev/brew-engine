# brew-engine — Architecture Reference

This document describes the internal structure of brew-engine: its layers,
the boundaries between them, every public and private interface, and a
step-by-step guide to adding a new subcommand.

---

## Table of Contents

- [brew-engine — Architecture Reference](#brew-engine--architecture-reference)
  - [Table of Contents](#table-of-contents)
  - [1. Layered Overview](#1-layered-overview)
  - [2. Layer Responsibilities](#2-layer-responsibilities)
    - [2.1 Entry point — `main`](#21-entry-point--main)
    - [2.2 Command layer — `cmd/`](#22-command-layer--cmd)
      - [The `Execute` safety net](#the-execute-safety-net)
    - [2.3 Cache layer — `internal/cache`](#23-cache-layer--internalcache)
    - [2.4 Contract layer — `internal/contract`](#24-contract-layer--internalcontract)
    - [2.5 Parser layer — `internal/parser`](#25-parser-layer--internalparser)
    - [2.6 Logger layer — `internal/logger`](#26-logger-layer--internallogger)
    - [2.7 Config layer — `internal/config`](#27-config-layer--internalconfig)
  - [3. Public Interfaces](#3-public-interfaces)
    - [`internal/contract`](#internalcontract)
    - [`internal/cache`](#internalcache)
    - [`internal/logger`](#internallogger)
    - [`internal/config`](#internalconfig)
    - [`internal/parser`](#internalparser)
    - [`cmd`](#cmd)
  - [4. Private / Injectable Interfaces](#4-private--injectable-interfaces)
  - [5. The JSON stdout Contract](#5-the-json-stdout-contract)
    - [Type taxonomy](#type-taxonomy)
    - [`is_stale` flag](#is_stale-flag)
    - [`"event"` stream](#event-stream)
    - [`"build_mode"` event](#build_mode-event)
  - [6. The Caching Layer — End-to-End Flow](#6-the-caching-layer--end-to-end-flow)
    - [`list` — infinite TTL, watcher-invalidated](#list--infinite-ttl-watcher-invalidated)
    - [`info` — 24h TTL, stale-while-revalidate](#info--24h-ttl-stale-while-revalidate)
  - [7. Log Infrastructure — Rotation, Merge, and Raw Output](#7-log-infrastructure--rotation-merge-and-raw-output)
    - [Directory layout](#directory-layout)
    - [Daily rotation (`dailyWriter`)](#daily-rotation-dailywriter)
    - [Monthly merge (`MergeOldMonths`)](#monthly-merge-mergeoldmonths)
    - [Raw brew output (`brew-output.log`)](#raw-brew-output-brew-outputlog)
  - [8. Adding a New Subcommand](#8-adding-a-new-subcommand)
    - [Step 1 — Define the contract types (if needed)](#step-1--define-the-contract-types-if-needed)
    - [Step 2 — Create `cmd/upgrade.go`](#step-2--create-cmdupgradego)
    - [Step 3 — If the command streams output, use `internal/parser`](#step-3--if-the-command-streams-output-use-internalparser)
    - [Step 4 — If the command needs caching, use `internal/cache`](#step-4--if-the-command-needs-caching-use-internalcache)
    - [Step 5 — Add injectable `execCommand` if the command shells out](#step-5--add-injectable-execcommand-if-the-command-shells-out)
    - [Step 6 — Write tests in `cmd/upgrade_test.go`](#step-6--write-tests-in-cmdupgrade_testgo)
    - [Step 7 — Update `cmd/root.go` doc comment and `README.md`](#step-7--update-cmdrootgo-doc-comment-and-readmemd)

---

## 1. Layered Overview

``` go
┌──────────────────────────────────────────────────────────┐
│                      Frontend (UI)                       │
│          SwiftUI app            Bubble Tea TUI           │
│       (subprocess via Process)  (direct Go import)       │
└───────────────────┬──────────────────────┬───────────────┘
                    │ stdout (JSON lines)  │ direct import
                    ▼                      ▼
┌──────────────────────────────────────────────────────────┐
│                     main.go                              │
│  config.Load() ──► logger.Init() ──► cmd.Execute()       │
└──────────────────────────────────────────────────────────┘
                    │
                    ▼
┌──────────────────────────────────────────────────────────┐
│                     cmd/  (Cobra)                        │
│  root.go  list.go  info.go  install.go  remove.go        │
│  watch.go  refresh.go  clean.go  nuke.go                 │
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
└─────────────────────┘              │  rotation.go        │
                    │                └─────────────────────┘
                    ▼
              exec.Command("brew", ...)

                    ▲  env vars set at startup
┌──────────────────────────────────────────────────────────┐
│                  internal/config                         │
│  defaults.go   config.go   (plutil → JSON → os.Setenv)   │
└──────────────────────────────────────────────────────────┘
```

go

**Invariant: `os.Stdout` receives only complete, newline-terminated
`contract.Response` JSON objects. Nothing else may be written to stdout
anywhere in the codebase.**

---

## 2. Layer Responsibilities

### 2.1 Entry point — `main`

`main.go` is intentionally minimal:

``` go
config.Load()          // 1. read plist → JSON → os.Setenv each key
logger.Init()          // 2. open daily log file; sets logger.Sugar and logger.Raw
// 3. brew check (see below)
cmd.Execute()          // 4. hands control to Cobra; never returns on success
```

**Step 3 — brew availability check (audit-logged):**

```
config.ResolvedBrewPath() → IsBrewExecutable?
  YES → logger.Sugar.Infow("brew verified", "path", ...)    → continue
  NO  → logger.Sugar.Errorw("brew not found", ...)           → emit brew_not_found JSON → exit 2
```

The check runs **after** `logger.Init()` so that both outcomes are written
to the Zap audit file (the daily log) on every invocation. Exit code 2 is
distinct from all other exits:

| Exit code | Meaning | Channel |
| --- | --- | --- |
| 0 | Success | — |
| 1 | Config or logger init failure | `stdout` JSON (`type:"fatal"`) + `stderr` plain text |
| 2 | Homebrew not found | `stdout` JSON (`type:"brew_not_found"`) + Zap log |

`config.Load()` runs first so that every env var the logger and commands
read (`BREW_ENGINE_LOG_DIR`, `BREW_ENGINE_LOG_LEVEL`, `BREW_TUI_CACHE_DIR`,
etc.) is already populated with plist-derived or compiled-default values
before any layer inspects them.

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
| --- | --- | --- |
| `root.go` | (root) | Emits JSON error on invocation with no subcommand; sets `SilenceErrors`/`SilenceUsage` |
| `list.go` | `list` | Cache-first (infinite TTL); falls back to `cache.BuildAndCacheList` |
| `info.go` | `info <pkg>` | Stale-while-revalidate (24h TTL); `--force` / `-f` bypasses cache |
| `install.go` | `install <pkg>` | Delegates directly to `parser.RunInstall`; streams progress events; invalidates list cache on success |
| `remove.go` | `remove <pkg>` | Delegates directly to `parser.RunRemove`; streams progress events; invalidates list cache on success |
| `watch.go` | `watch` | Starts `cache.StartWatcher`; blocks on `signal.NotifyContext` until SIGINT/SIGTERM |
| `refresh.go` | `refresh` | Invalidates list cache and forces a rebuild; emits `Type="list"` |
| `clean.go` | `clean` | Wipes log and/or cache directories on demand; emits `Type="clean"` |
| `nuke.go` | `nuke` | Runs `brew cleanup -s --prune=all` and/or wipes app cache; emits `Type="nuke"` |

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
| --- | --- |
| `cache.go` | Read/write/invalidate flat JSON files; `BuildAndCacheList` orchestration |
| `watcher.go` | `fsnotify` watcher lifecycle, debounce loop, background list rebuild |

**Cache directory resolution** (`CacheDir`):

1. `BREW_TUI_CACHE_DIR` env var (verbatim, no tilde expansion).
2. `~/Library/Application Support/BrewExplorer/cache` — macOS compiled default.
3. `/tmp/brew-engine/cache/` (fallback when `os.UserHomeDir` fails).

**Namespace layout on disk:**

``` go
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
| --- | --- | --- |
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

Wraps `go.uber.org/zap`. Divided across two files:

| File | Concern |
| --- | --- |
| `logger.go` | Package-level singletons, `Init`, `Sync`, `LogDir`, log-level control, session/request ID injection, `OpenBrewOutputLog` |
| `rotation.go` | `dailyWriter` (size + date rotation), `MergeOldMonths`, file-path helpers |

**Package-level singletons:**

| Var | Type | Use |
| --- | --- | --- |
| `logger.Sugar` | `*zap.SugaredLogger` | Structured key-value logging in commands and cache |
| `logger.Raw` | `*zap.Logger` | High-performance structured logging (parser hot path) |
| `logger.Level` | `zapcore.Level` | Current resolved level; callers check before building expensive debug payloads |

**Log-level control** (`BREW_ENGINE_LOG_LEVEL`):

| Value | Behaviour |
| --- | --- |
| `debug` | Full execution trace — every brew output line, every cache decision |
| `info` (default) | Key markers only — start/end, cache hits, errors, build-mode detection |
| `warn` / `error` | Progressively quieter |

**Session/request correlation** — when `BREW_ENGINE_SESSION_ID` and/or
`BREW_ENGINE_REQUEST_ID` are set, the values are injected as base fields
into every Zap entry. This enables cross-layer correlation between the UI
and engine without modifying any call sites.

**Log directory resolution** (`resolveLogDir`, highest priority first):

1. `BREW_ENGINE_LOG_DIR` — set by `internal/config` from the plist at startup.
2. `BREW_TUI_LOG_DIR` — legacy override, honoured for backward compatibility.
3. `~/Library/Application Support/BrewExplorer/logs` — macOS compiled default.
4. `/tmp/brew-engine/` — last-resort fallback when `os.UserHomeDir` fails.

`logger.Sugar` is `nil` until `logger.Init()` is called. All callers
guard: `if logger.Sugar != nil { … }` — the logger is always optional,
never required for correct operation.

---

### 2.7 Config layer — `internal/config`

Loads configuration once at process startup, before the logger or any
subcommand runs.

| File | Concern |
| --- | --- |
| `defaults.go` | Compiled-default constants: bundle ID, plist key names, env var names, fallback path values |
| `config.go` | `Load()` entry point: resolve plist path → shell out to `plutil` → parse JSON → `os.Setenv` each key |

**Priority chain (lowest → highest):**

```
compiled defaults  <  plist values  <  env vars already set in the process
```

`os.Setenv` is called only when the env var is **not already set**, so an
operator or test that exports `BREW_ENGINE_LOG_DIR` before launching the
binary always wins.

**Plist location:** `~/Library/Preferences/com.mobilityquarks.brewexplorer.plist`

**Key schema:**

| Plist key | Env var set | Default value |
| --- | --- | --- |
| `LogDir` | `BREW_ENGINE_LOG_DIR` | `~/Library/Application Support/BrewExplorer/logs` |
| `LogFileName` | `BREW_ENGINE_LOG_FILE_NAME` | `brew-engine.log` |
| `BrewOutputLogFileName` | `BREW_ENGINE_BREW_OUTPUT_LOG_FILE_NAME` | `brew-output.log` |
| `LogLevel` | `BREW_ENGINE_LOG_LEVEL` | `info` |
| `CacheDir` | `BREW_TUI_CACHE_DIR` | `~/Library/Application Support/BrewExplorer/cache` |
| `ListCacheFileName` | `BREW_ENGINE_LIST_CACHE_FILE_NAME` | `list.json` |
| `InfoCacheDirName` | `BREW_ENGINE_INFO_CACHE_DIR_NAME` | `info` |
| `BrewPath` | `BREW_ENGINE_BREW_PATH` | resolved via fallback chain (see below) |

**Brew binary resolution** (first executable path wins):

1. `BrewPath` plist key (absolute path, must be executable).
2. `/opt/homebrew/bin/brew` (Apple Silicon default).
3. `/usr/local/bin/brew` (Intel default).
4. `exec.LookPath("brew")` (PATH search).

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
type FormulaInfo    struct { ... }
type CaskInfo       struct { ... }
type ListData       struct { Formulae []FormulaInfo; Casks []CaskInfo; Total int }
type NamesList      struct { Formulae []string; Casks []string; Total int }
type CacheEvent     struct { Action string; Target string }
type BuildModeData  struct { Package string; Mode string }          // "bottle" | "source"
type ProgressStep   struct { Package string; Step string }
type DoneData       struct { Package string; ExitCode int; BuildMode string }
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
var Level zapcore.Level           // current resolved level

func Init() error                 // open daily log; trigger monthly merge
func Sync()
func LogDir() string
func DebugEnabled() bool          // guard for expensive debug payload construction
func OpenBrewOutputLog(command string) *os.File   // open brew-output.log with header
func WriteBrewOutputFooter(f *os.File, exitCode int)  // write [EXIT N] footer
func MergeOldMonths(logDir string)  // merge prior-month daily files into monthly/
```

### `internal/config`

```go
func Load() error  // read plist via plutil, export env vars, resolve brew path
func PlistPath() string  // ~/Library/Preferences/com.mobilityquarks.brewexplorer.plist
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
| --- | --- | --- | --- |
| `execCommand` | `internal/cache` | `exec.Command` | Inject a fake `brew` binary |
| `userHomeDir` | `internal/cache` | `os.UserHomeDir` | Simulate a missing home directory |
| `newFSWatcher` | `internal/cache` | `fsnotify.NewWatcher` | Simulate watcher creation failure |
| `execBrewCommand` | `internal/parser` | `exec.Command` | Inject a fake `brew` binary |
| `execBrewInfo` | `cmd` (info.go) | `exec.Command` | Inject fake `brew info` JSON output |
| `execNukeCommand` | `cmd` (nuke.go) | `exec.Command` | Inject fake `brew cleanup` response |
| `userHomeDir` | `internal/logger` | `os.UserHomeDir` | Simulate a missing home directory |
| `currentDate` | `internal/logger` | `time.Now().Format(…)` | Control date in rotation tests |
| `userHomeDir` | `internal/config` | `os.UserHomeDir` | Simulate a missing home directory |
| `execPlutil` | `internal/config` | shells out to `plutil` | Inject fake plist JSON output |

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

``` go
{"success":true|false,"type":"<type>","is_stale":true,"error":"<msg>","data":{…}}\n
```

### Type taxonomy

| `type` | `data` shape | Emitted by |
| --- | --- | --- |
| `"list"` | `NamesList` | `cmd/list.go`, `cmd/refresh.go` |
| `"info"` | `FormulaInfo` or `CaskInfo` | `cmd/info.go` |
| `"build_mode"` | `BuildModeData` | `internal/parser` (once per install, on first decisive `==>` line) |
| `"progress"` | `ProgressStep` | `internal/parser` (during install/remove) |
| `"done"` | `DoneData` (with `build_mode` field) | `internal/parser` (on exit 0) |
| `"error"` | `DoneData` (with `build_mode` field, optional) | any layer on failure |
| `"event"` | `CacheEvent` | `internal/cache` watcher on rebuild |
| `"clean"` | `cleanResult` | `cmd/clean.go` on successful wipe |
| `"brew_not_found"` | `BrewNotFoundData` | `main()` pre-subcommand brew check; process exits 2 after this event |
| `"nuke"` | `NukeData` | `cmd/nuke.go`; `brew_exit_code` reflects brew cleanup's exit status |
| `"fatal"` | none | `main()` on config or logger init failure; `error` field carries detail; process exits 1 |

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

### `"build_mode"` event

Emitted **at most once** per install operation, by `internal/parser`, as soon
as the first decisive `==>` line is seen in the brew output stream.

```json
{"success":true,"type":"build_mode","data":{"package":"wget","mode":"bottle"}}
{"success":true,"type":"build_mode","data":{"package":"wget","mode":"source"}}
```

| `mode` | Meaning | UI guidance |
| --- | --- | --- |
| `"bottle"` | Pre-compiled binary is being poured | Spinner; expect ~5 s |
| `"source"` | No bottle available; compiling from source | Progress bar + "may take several minutes" warning |

The terminal `"done"` / `"error"` event also carries `build_mode` in its
`DoneData` payload so the frontend can record or display a post-install
summary without tracking state from an earlier event:

```json
{"success":true,"type":"done","data":{"package":"wget","exit_code":0,"build_mode":"source"}}
```

**Detection heuristics** (case-insensitive `==>` line matching in `internal/parser`):

| Pattern | Detected mode |
| --- | --- |
| `==> Pouring *.bottle.*` | `bottle` |
| `==> Installing dependencies for …` | `source` |
| `==> Installing <pkg> dependency: …` | `source` |
| `==> ./configure …` | `source` |
| `==> cmake …` | `source` |
| `==> make …` | `source` |

All other `==>` lines (e.g. `==> Downloading`, `==> Installing wget`) are
non-decisive and do not trigger the event. If the process exits before any
decisive line is observed, `build_mode` is omitted from `DoneData`.

---

## 6. The Caching Layer — End-to-End Flow

### `list` — infinite TTL, watcher-invalidated

``` go
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

``` go
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

## 7. Log Infrastructure — Rotation, Merge, and Raw Output

### Directory layout

```
<LogDir>/
├── daily/
│   ├── brew-engine-2026-03-30.log      ← primary file for that day
│   ├── brew-engine-2026-03-30.0.log    ← overflow #0 when primary exceeds 2 MB
│   ├── brew-engine-2026-03-30.1.log    ← overflow #1, etc.
│   └── brew-engine-2026-03-31.log      ← next day starts fresh
├── monthly/
│   └── brew-engine-2026-02.log         ← all February daily files merged here
└── brew-output.log                     ← raw stdout/stderr from every brew subprocess
```

`<LogDir>` resolves as described in [section 2.6](#26-logger-layer--internallogger).

### Daily rotation (`dailyWriter`)

`dailyWriter` is a `zapcore.WriteSyncer` wrapping an `*os.File`. It is
passed to `zapcore.Lock` so the underlying Zap core can use it safely from
parallel goroutines.

Two rotation triggers, both checked on every `Write` call:

| Trigger | Action |
| --- | --- |
| Calendar day changed (`currentDate() != w.currentDate`) | `rotateToDate(newDate)` — probe for the highest existing suffix file for the new date (or create the base file if none exists) |
| Write would exceed 2 MB (`currentSize + len(p) > maxLogFileSize`) | `rotateToNextSuffix()` — always create a **new** file with the next numeric suffix; never reopen the existing one |

The two cases use separate methods to keep the logic explicit:

- `rotateToDate` calls `openLogFileForDate`, which probes the directory and
  advances the suffix only if the latest existing file for that date is
  already full.
- `rotateToNextSuffix` unconditionally increments `currentSuffix` and opens
  a fresh file — it must never reopen the file that just filled up.

### Monthly merge (`MergeOldMonths`)

Called once inside `Init()`, before the daily writer opens today's file.

```
MergeOldMonths(logDir)
    │
    ├── scan <LogDir>/daily/ for files matching brew-engine-YYYY-MM-DD[.N].log
    ├── group by YYYY-MM
    ├── skip the current month
    └── for each prior month:
            ├── sort files chronologically
            ├── append each file to <LogDir>/monthly/brew-engine-YYYY-MM.log
            └── delete the daily files after a successful merge
```

Merge failures are silently skipped so that a permission error on an old
log file never prevents the engine from starting.

### Raw brew output (`brew-output.log`)

**Every command that invokes a brew subprocess** appends its raw output to
`<LogDir>/brew-output.log` via `logger.OpenBrewOutputLog` /
`logger.WriteBrewOutputFooter`. This is an **engine-wide invariant** — not
just streaming commands.

| Command / code path | Brew subprocess | Output captured |
| --- | --- | --- |
| `brew-engine install <pkg>` | `brew install -v <pkg>` | lines streamed via pipe (ANSI-stripped in Zap; raw in brew-output.log) |
| `brew-engine remove <pkg>` | `brew uninstall <pkg>` | same as install |
| `brew-engine info <pkg>` (cache miss or stale) | `brew info --json=v2 <pkg>` | captured via `cmd.Output()` + `ExitError.Stderr` |
| `brew-engine list` (cache miss) | `brew list --formula` + `brew list --cask` | captured via `cmd.Output()` + `ExitError.Stderr` |
| `brew-engine refresh` | `brew list --formula` + `brew list --cask` | same as list |
| `brew-engine nuke [--brew\|--all]` | `brew cleanup -s --prune=all` | captured via `cmd.CombinedOutput()` |

Commands that do **not** invoke brew directly (`clean`, `watch`) do not write
to `brew-output.log`.

**Canonical file format** (one block per invocation, appended):

```
[2026-03-30T07:21:00Z] /usr/local/bin/brew install wget
==> Downloading https://…
==> Pouring wget--2.4.1.arm64_sequoia.bottle.tar.gz
[EXIT 0]

[2026-03-30T07:22:10Z] /usr/local/bin/brew list --formula
git
wget
zsh
[EXIT 0]
```

The full brew binary path (from `BREW_ENGINE_BREW_PATH`) is used in the
header, not just `brew`, so the log is unambiguous on systems with multiple
Homebrew installs.

The file is closed and the footer written after the subprocess exits, so
partial writes cannot occur even if the engine is interrupted mid-command.

---

## 8. Adding a New Subcommand

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
| --- | --- |
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
| --- | --- |
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
- The `Type taxonomy` table in [section 5](#5-the-json-stdout-contract) of this document.
- The **Build & Run** usage examples in `README.md`.
