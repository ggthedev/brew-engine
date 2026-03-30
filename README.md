# brew-engine

A headless, single-binary Go backend that wraps the `brew` CLI and communicates
exclusively via newline-delimited JSON on **stdout**.

It is the engine layer of **BrewExplorer** — a decoupled Homebrew package
management interface with two planned frontends:

- **Phase 1** — Native macOS SwiftUI app (launches `brew-engine` as a subprocess via `Process`)
- **Phase 2** — Terminal UI built with [Bubble Tea](https://github.com/charmbracelet/bubbletea) (imports engine packages directly)

---

## Architecture

```
┌─────────────────────────────────┐
│         Frontend (UI)           │
│  SwiftUI app  │  Bubble Tea TUI │
└──────────┬────┴────────┬────────┘
           │  stdout     │ direct import
           │  (JSON)     │
     ┌─────▼─────────────▼───────┐
     │        brew-engine        │
     │  cmd/  →  internal/       │
     │  Cobra    parser  logger  │
     │           contract        │
     └──────────────┬────────────┘
                    │ exec.Command
                    ▼
        ┌──────────────────────────┐
        │  /opt/homebrew/bin/brew  │
        └──────────────────────────┘
```

### Key design decisions

| Decision | Rationale |
|---|---|
| **stdout = JSON only** | Any consumer (Swift `Process`, Go TUI) reads a single, predictable stream |
| **stderr = internal use** | Cobra's usage/error output and OS-level noise never contaminate the JSON channel |
| **Synchronous install** | Homebrew holds a system-wide lock; parallelism adds complexity with zero benefit in V1 |
| **Log file, not stderr** | Raw `brew` output (ANSI stripped) is persisted for the user to inspect; never sent to the UI |
| **Direct distribution** | Avoids macOS App Sandbox restrictions on Homebrew's directories |

---

## JSON Communication Contract

Every line written to stdout is a `contract.Response`:

```json
{ "success": true|false, "type": "...", "error": "...", "data": { ... } }
```

### `type` values

| type | trigger | `data` shape |
|---|---|---|
| `list` | `brew-engine list` / `brew-engine refresh` | `NamesList` — `{formulae:["..."], casks:["..."], total:N}` |
| `info` | `brew-engine info <pkg>` | `FormulaInfo` or `CaskInfo` |
| `build_mode` | during `install`, on first decisive `==>` line | `BuildModeData` |
| `progress` | during `install` / `remove` | `ProgressStep` |
| `done` | install/remove finished (exit 0) | `DoneData` |
| `error` | any failure | `DoneData` (exit ≠ 0) or omitted |
| `event` | `brew-engine watch` detects external mutation | `CacheEvent` |
| `clean` | `brew-engine clean` | `cleanResult` — dirs wiped |
| `nuke` | `brew-engine nuke [--brew\|--app\|--all]` | `NukeData` — flags indicating what was cleaned |
| `brew_not_found` | startup, when Homebrew cannot be located | `BrewNotFoundData` — checked paths; process exits 2 |
| `fatal` | startup, config or logger init failure | no `data`; `error` field contains detail; process exits 1 |

### Example responses

**list**
```json
{"success":true,"type":"list","data":{"formulae":["git","wget","zsh"],"casks":["firefox","iterm2"],"total":5}}
```

**progress** (streamed line-by-line during install)
```json
{"success":true,"type":"progress","data":{"package":"wget","step":"==> Downloading https://ghcr.io/v2/homebrew/core/wget/manifests/1.21.4"}}
{"success":true,"type":"progress","data":{"package":"wget","step":"==> Pouring wget--1.21.4.arm64_ventura.bottle.tar.gz"}}
```

**done**
```json
{"success":true,"type":"done","data":{"package":"wget","exit_code":0,"build_mode":"bottle"}}
```

**build_mode** (emitted once per install, on the first decisive `==>` line)
```json
{"success":true,"type":"build_mode","data":{"package":"wget","mode":"bottle"}}
{"success":true,"type":"build_mode","data":{"package":"wget","mode":"source"}}
```

**error**
```json
{"success":false,"type":"error","error":"brew install exited with code 1","data":{"package":"wget","exit_code":1,"build_mode":"source"}}
```

---

## Logging

Two log files are maintained under `<LogDir>` (default: `~/Library/Application Support/BrewExplorer/logs`):

| File | Contents | Env var override |
|---|---|---|
| `brew-engine.log` | Structured Zap entries — cache decisions, lifecycle events, errors | `BREW_ENGINE_LOG_DIR` |
| `brew-output.log` | Raw stdout+stderr from **every** `brew` subprocess invocation | same dir |

The log directory resolves from (highest priority first): `BREW_ENGINE_LOG_DIR` → `BREW_TUI_LOG_DIR` (legacy) → `~/Library/Application Support/BrewExplorer/logs` → `/tmp/brew-engine/`.

No raw text ever reaches stdout — only newline-terminated JSON objects.

---

## Build & Run

### Makefile targets

| Target | Output | Use case |
|---|---|---|
| `make` / `make build` | `build/brew-engine` | Native arch — fast dev cycle |
| `make build-arm64` | `build/brew-engine-arm64` | Apple Silicon native binary |
| `make build-amd64` | `build/brew-engine-amd64` | Intel Mac native binary |
| `make build-universal` | `build/brew-engine-universal` | Fat binary (both arches via `lipo`) — **use for app bundle distribution** |
| `make test` | — | Run full test suite |
| `make clean` | — | Remove `build/` directory |

> `build-universal` requires `lipo` (included with Xcode Command Line Tools).

```bash
# Fetch dependencies and build (native arch)
make

# Or manually
go mod tidy
go build -o build/brew-engine .

# Usage
./build/brew-engine list                  # list installed formulae + casks (NamesList)
./build/brew-engine refresh               # force cache rebuild, emit fresh list
./build/brew-engine info wget             # detailed info (stale-while-revalidate)
./build/brew-engine install wget          # stream progress → done/error
./build/brew-engine remove wget           # stream progress → done/error
./build/brew-engine watch                 # block; emit "event" on external mutations
./build/brew-engine clean --logs          # wipe log directory
./build/brew-engine clean --cache         # wipe cache directory
./build/brew-engine nuke                  # brew cleanup -s --prune=all (default: --brew)
./build/brew-engine nuke --app            # wipe app cache directory
./build/brew-engine nuke --all            # brew cleanup + wipe app cache
```

---

## Project layout

```
brew-engine/
├── main.go                     # Entry point: config.Load → logger.Init → brew check → cmd.Execute
├── go.mod
├── Makefile
├── Docs/
│   ├── ARCHITECTURE.md         # Full internal reference
│   └── SWIFT_INTEGRATION.md    # Swift macOS frontend integration guide
├── cmd/
│   ├── root.go                 # Cobra root; SilenceErrors/Usage; Execute()
│   ├── list.go                 # Cache-first list; fallback to BuildAndCacheList
│   ├── refresh.go              # Invalidate + rebuild list cache; emits Type="list"
│   ├── info.go                 # Stale-while-revalidate 24h TTL; --force flag
│   ├── install.go              # Delegates to parser.RunInstall; streams progress
│   ├── remove.go               # Delegates to parser.RunRemove; streams progress
│   ├── watch.go                # fsnotify watcher; blocks until SIGINT/SIGTERM
│   ├── clean.go                # Wipe log/cache dirs on demand; emits Type="clean"
│   └── nuke.go                 # brew cleanup + app cache wipe; emits Type="nuke"
└── internal/
    ├── cache/
    │   ├── cache.go            # CacheDir, Read/Write/Invalidate, BuildAndCacheList
    │   └── watcher.go          # StartWatcher, runWatcher (debounce), rebuildListCache
    ├── config/
    │   ├── config.go           # Load(): plist → plutil → os.Setenv; priority chain
    │   └── defaults.go         # Compiled-default constants (paths, env var names)
    ├── contract/
    │   └── types.go            # All JSON structs + WriteJSON helper
    ├── logger/
    │   ├── logger.go           # Zap init, brew-output.log helpers, session correlation
    │   └── rotation.go         # dailyWriter (size+date rotation), MergeOldMonths
    └── parser/
        └── parser.go           # bufio.Scanner, ANSI strip, build-mode detection, progress events
```
