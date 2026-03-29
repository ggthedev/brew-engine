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
     ┌─────▼─────────────▼──────┐
     │        brew-engine        │
     │  cmd/  →  internal/       │
     │  Cobra    parser  logger  │
     │           contract        │
     └──────────────┬────────────┘
                    │ exec.Command
              ┌─────▼──────┐
              │  /opt/homebrew/bin/brew  │
              └────────────┘
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
| `list` | `brew-engine list` | `NamesList` |
| `info` | `brew-engine info <pkg>` | `FormulaInfo` or `CaskInfo` |
| `build_mode` | during `install`, on first decisive `==>` line | `BuildModeData` |
| `progress` | during `install` / `remove` | `ProgressStep` |
| `done` | install/remove finished (exit 0) | `DoneData` |
| `error` | any failure | `DoneData` (on exit ≠ 0) or omitted |
| `event` | `brew-engine watch` detects external mutation | `CacheEvent` |

### Example responses

**list**
```json
{"success":true,"type":"list","data":{"formulae":[{"name":"wget","full_name":"wget","tap":"homebrew/core","desc":"Internet file retriever","homepage":"https://www.gnu.org/software/wget/","version":"1.21.4","installed_version":"1.21.4","installed":true,"outdated":false,"pinned":false}],"casks":[],"total":1}}
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

All raw `brew` output (stdout + stderr, ANSI stripped) is written to a
structured JSON log file. No raw text ever reaches stdout.

| Config | Value |
|---|---|
| Env var | `BREW_TUI_LOG_DIR` |
| Default path | `~/.local/state/brew-engine/brew-engine.log` |

---

## Build & Run

```bash
# Fetch dependencies and build
make

# Or manually
go mod tidy
go build -o build/brew-engine .

# Usage
./build/brew-engine list
./build/brew-engine info wget
./build/brew-engine install wget
./build/brew-engine remove wget
```

---

## Project layout

```
brew-engine/
├── main.go                     # Entry point: init logger → cmd.Execute()
├── go.mod
├── Makefile
├── cmd/
│   ├── root.go                 # Cobra root; SilenceErrors/Usage; Execute()
│   ├── list.go                 # Cache-first list; fallback to BuildAndCacheList
│   ├── info.go                 # Stale-while-revalidate; --force flag
│   ├── install.go              # delegates to parser.RunInstall
│   ├── remove.go               # delegates to parser.RunRemove
│   └── watch.go                # fsnotify watcher; blocks until SIGINT/SIGTERM
└── internal/
    ├── cache/
    │   ├── cache.go            # CacheDir, Read/Write/Invalidate, BuildAndCacheList
    │   └── watcher.go          # StartWatcher, runWatcher (debounce), rebuildListCache
    ├── contract/
    │   └── types.go            # All JSON structs + WriteJSON helper
    ├── logger/
    │   └── logger.go           # Zap file logger, BREW_TUI_LOG_DIR
    └── parser/
        └── parser.go           # bufio.Scanner, ANSI strip, build-mode detection, progress events
```
