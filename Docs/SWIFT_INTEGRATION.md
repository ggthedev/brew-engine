# brew-engine — Swift macOS Integration Guide

This document shows a native Swift macOS app how to communicate with
`brew-engine`. All examples use `Foundation.Process` (previously `NSTask`)
and standard `Pipe` / `FileHandle` APIs available in every macOS target
without additional dependencies.

---

## Table of Contents

- [How communication works](#how-communication-works)
- [1. Shared contract types](#1-shared-contract-types)
- [2. Spawning brew-engine](#2-spawning-brew-engine)
- [3. One-shot commands (list, refresh, info, nuke, clean)](#3-one-shot-commands)
  - [3.1 list — installed packages](#31-list--installed-packages)
  - [3.2 refresh — force cache rebuild](#32-refresh--force-cache-rebuild)
  - [3.3 info — package detail](#33-info--package-detail)
  - [3.4 nuke — brew cleanup](#34-nuke--brew-cleanup)
  - [3.5 clean — wipe logs or cache](#35-clean--wipe-logs-or-cache)
- [4. Streaming commands (install, remove)](#4-streaming-commands-install-remove)
- [5. Watch — background mutation events](#5-watch--background-mutation-events)
- [6. Correlation IDs (session / request)](#6-correlation-ids-session--request)
- [7. Configuration via plist](#7-configuration-via-plist)
- [8. Error handling patterns](#8-error-handling-patterns)
- [9. Embedding brew-engine in the app bundle](#9-embedding-brew-engine-in-the-app-bundle)

---

## How communication works

```
SwiftUI App
    │
    │  Foundation.Process (subprocess)
    ▼
brew-engine  ──stdout──►  Pipe  ──►  FileHandle  ──►  Swift JSON decoder
             ──stderr──►  /dev/null  (internal only; never user data)
```

- **stdout** carries only newline-terminated `contract.Response` JSON objects.
- **stderr** is for internal Cobra/OS noise. Redirect it to `/dev/null` or
  a separate pipe if you want to inspect it.
- Each invocation is a short-lived process **except** `watch`, which stays
  alive until your app terminates it.

---

## 1. Shared contract types

Declare these `Decodable` structs once in your Swift target. They mirror
`internal/contract/types.go` exactly.

```swift
import Foundation

// MARK: - Envelope

/// Every response brew-engine writes to stdout is wrapped in this envelope.
struct BrewResponse: Decodable {
    let success: Bool
    let type: String
    let isStale: Bool?
    let error: String?

    enum CodingKeys: String, CodingKey {
        case success, type, error
        case isStale = "is_stale"
    }
}

// MARK: - Data payloads

struct NamesList: Decodable {
    let formulae: [String]
    let casks: [String]
    let total: Int
}

struct FormulaInfo: Decodable {
    let name: String
    let fullName: String
    let tap: String
    let description: String
    let homepage: String
    let version: String
    let installedVersion: String?
    let installed: Bool
    let outdated: Bool
    let pinned: Bool

    enum CodingKeys: String, CodingKey {
        case name, tap, homepage, version, installed, outdated, pinned
        case fullName         = "full_name"
        case description      = "desc"
        case installedVersion = "installed_version"
    }
}

struct CaskInfo: Decodable {
    let token: String
    let fullToken: String
    let tap: String
    let name: String
    let description: String
    let homepage: String
    let version: String
    let installed: Bool
    let outdated: Bool

    enum CodingKeys: String, CodingKey {
        case token, tap, name, homepage, version, installed, outdated
        case fullToken   = "full_token"
        case description = "desc"
    }
}

struct ProgressStep: Decodable {
    let package: String
    let step: String
}

struct DoneData: Decodable {
    let package: String
    let exitCode: Int
    let buildMode: String?

    enum CodingKeys: String, CodingKey {
        case package
        case exitCode  = "exit_code"
        case buildMode = "build_mode"
    }
}

struct BuildModeData: Decodable {
    let package: String
    let mode: String   // "bottle" | "source"
}

struct CacheEvent: Decodable {
    let action: String  // "cache_rebuilt"
    let target: String  // "list"
}

struct NukeData: Decodable {
    let brewCacheNuked: Bool?
    let appCacheNuked: Bool?

    enum CodingKeys: String, CodingKey {
        case brewCacheNuked = "brew_cache_nuked"
        case appCacheNuked  = "app_cache_nuked"
    }
}

struct BrewNotFoundData: Decodable {
    let checkedPaths: [String]

    enum CodingKeys: String, CodingKey {
        case checkedPaths = "checked_paths"
    }
}
```

---

## 2. Spawning brew-engine

A reusable helper that runs brew-engine with arbitrary arguments and returns
the raw stdout lines:

```swift
import Foundation

enum BrewEngineError: Error, LocalizedError {
    case binaryNotFound(String)
    case brewNotFound([String])
    case processFailure(Int32)
    case decodeError(String, Error)

    var errorDescription: String? {
        switch self {
        case .binaryNotFound(let path): return "brew-engine not found at \(path)"
        case .brewNotFound(let paths):  return "Homebrew not found. Checked: \(paths.joined(separator: ", "))"
        case .processFailure(let code): return "brew-engine exited with code \(code)"
        case .decodeError(let line, _): return "Could not decode JSON: \(line)"
        }
    }
}

/// Locates the brew-engine binary embedded in the app bundle.
/// Falls back to a development path when running from Xcode.
func brewEnginePath() -> String {
    // Production: binary is in Contents/MacOS/ next to the app executable.
    if let bundlePath = Bundle.main.executableURL?
        .deletingLastPathComponent()
        .appendingPathComponent("brew-engine")
        .path,
       FileManager.default.isExecutableFile(atPath: bundlePath) {
        return bundlePath
    }
    // Development fallback: built via `make` in the sibling repo directory.
    return "\(NSHomeDirectory())/path/to/brew-engine/build/brew-engine"
}

/// Runs brew-engine with the given arguments, collects all stdout lines,
/// and returns them. Blocks the calling thread — always call from a background
/// queue or inside `Task { }`.
///
/// - Parameters:
///   - args: Subcommand and flags, e.g. `["list"]` or `["info", "wget"]`.
///   - env:  Additional environment variables to merge into the process env.
/// - Returns: Array of raw stdout line strings (newline stripped).
/// - Throws: `BrewEngineError.binaryNotFound` if the binary is missing.
func runBrewEngine(args: [String], env: [String: String] = [:]) throws -> [String] {
    let binary = brewEnginePath()
    guard FileManager.default.isExecutableFile(atPath: binary) else {
        throw BrewEngineError.binaryNotFound(binary)
    }

    let process = Process()
    process.executableURL = URL(fileURLWithPath: binary)
    process.arguments = args

    // Merge caller-supplied env on top of the inherited process environment.
    var environment = ProcessInfo.processInfo.environment
    env.forEach { environment[$0] = $1 }
    process.environment = environment

    let stdoutPipe = Pipe()
    let stderrPipe = Pipe()   // capture to avoid console noise
    process.standardOutput = stdoutPipe
    process.standardError  = stderrPipe

    try process.run()
    process.waitUntilExit()

    let data = stdoutPipe.fileHandleForReading.readDataToEndOfFile()
    let raw = String(data: data, encoding: .utf8) ?? ""
    return raw
        .components(separatedBy: "\n")
        .map { $0.trimmingCharacters(in: .whitespaces) }
        .filter { !$0.isEmpty }
}
```

---

## 3. One-shot commands

### 3.1 list — installed packages

Returns a single JSON line with `type:"list"` and a `NamesList` payload.

```swift
func fetchInstalledPackages() async throws -> NamesList {
    let lines = try await Task.detached(priority: .userInitiated) {
        try runBrewEngine(args: ["list"])
    }.value

    guard let firstLine = lines.first else {
        throw BrewEngineError.processFailure(-1)
    }
    return try decodePayload(NamesList.self, from: firstLine, expectedType: "list")
}

// Example usage in a ViewModel:
//   let packages = try await fetchInstalledPackages()
//   print(packages.formulae)   // ["git", "wget", "zsh"]
//   print(packages.casks)      // ["firefox", "iterm2"]
```

### 3.2 refresh — force cache rebuild

Identical shape to `list` but always invokes brew, bypassing the cache.

```swift
func refreshInstalledPackages() async throws -> NamesList {
    let lines = try await Task.detached(priority: .userInitiated) {
        try runBrewEngine(args: ["refresh"])
    }.value

    guard let firstLine = lines.first else {
        throw BrewEngineError.processFailure(-1)
    }
    return try decodePayload(NamesList.self, from: firstLine, expectedType: "list")
}
```

### 3.3 info — package detail

Returns one or two JSON lines. The first line is the cached (possibly stale)
data; if `is_stale` is `true`, a second fresh line follows immediately.

```swift
enum PackageInfo {
    case formula(FormulaInfo)
    case cask(CaskInfo)
}

/// Fetches info for `pkg`, handling the stale-while-revalidate pattern.
/// `onStale` is called with the cached copy while the fresh fetch runs;
/// the return value is always the most up-to-date response.
func fetchPackageInfo(
    pkg: String,
    force: Bool = false,
    onStale: ((PackageInfo) -> Void)? = nil
) async throws -> PackageInfo {
    var args = ["info", pkg]
    if force { args.append("--force") }

    let lines = try await Task.detached(priority: .userInitiated) {
        try runBrewEngine(args: args)
    }.value

    var result: PackageInfo?

    for line in lines {
        guard let data = line.data(using: .utf8) else { continue }
        let envelope = try JSONDecoder().decode(BrewResponse.self, from: data)

        guard envelope.success, envelope.type == "info" else {
            throw BrewEngineError.processFailure(-1)
        }

        // Decode the data field as formula first, then cask.
        if let info = tryDecode(FormulaInfo.self, fromRaw: data) {
            let pkg = PackageInfo.formula(info)
            if envelope.isStale == true {
                onStale?(pkg)   // render immediately; fresh copy follows
            } else {
                result = pkg
            }
        } else if let info = tryDecode(CaskInfo.self, fromRaw: data) {
            let pkg = PackageInfo.cask(info)
            if envelope.isStale == true {
                onStale?(pkg)
            } else {
                result = pkg
            }
        }
    }

    guard let final = result else { throw BrewEngineError.processFailure(-1) }
    return final
}

// MARK: - Helpers

/// Decodes the `data` field of a `contract.Response` JSON blob.
func decodePayload<T: Decodable>(_ type: T.Type, from line: String, expectedType: String) throws -> T {
    guard let raw = line.data(using: .utf8) else {
        throw BrewEngineError.decodeError(line, NSError())
    }
    // Decode envelope to check for engine-level errors first.
    let envelope = try JSONDecoder().decode(BrewResponse.self, from: raw)
    if !envelope.success {
        throw BrewEngineError.processFailure(-1)
    }
    guard envelope.type == expectedType else {
        throw BrewEngineError.processFailure(-1)
    }

    // Re-decode the full object to extract the data field.
    struct Wrapper<T: Decodable>: Decodable { let data: T }
    let wrapper = try JSONDecoder().decode(Wrapper<T>.self, from: raw)
    return wrapper.data
}

/// Attempts to decode `T` from a raw response blob; returns nil on failure.
func tryDecode<T: Decodable>(_ type: T.Type, fromRaw raw: Data) -> T? {
    struct Wrapper<T: Decodable>: Decodable { let data: T }
    return try? JSONDecoder().decode(Wrapper<T>.self, from: raw).data
}
```

### 3.4 nuke — brew cleanup

```swift
struct NukeResult {
    let brewCacheNuked: Bool
    let appCacheNuked: Bool
}

enum NukeScope { case brew, app, all }

func nukeCache(scope: NukeScope = .brew) async throws -> NukeResult {
    var args = ["nuke"]
    switch scope {
    case .brew: args.append("--brew")
    case .app:  args.append("--app")
    case .all:  args.append("--all")
    }

    let lines = try await Task.detached(priority: .userInitiated) {
        try runBrewEngine(args: args)
    }.value

    guard let firstLine = lines.first else { throw BrewEngineError.processFailure(-1) }
    let payload = try decodePayload(NukeData.self, from: firstLine, expectedType: "nuke")
    return NukeResult(
        brewCacheNuked: payload.brewCacheNuked ?? false,
        appCacheNuked:  payload.appCacheNuked  ?? false
    )
}
```

### 3.5 clean — wipe logs or cache

```swift
enum CleanTarget { case logs, cache, all }

func cleanStorage(target: CleanTarget) async throws {
    var args = ["clean"]
    switch target {
    case .logs:  args.append("--logs")
    case .cache: args.append("--cache")
    case .all:   args += ["--logs", "--cache"]
    }
    _ = try await Task.detached(priority: .utility) {
        try runBrewEngine(args: args)
    }.value
}
```

---

## 4. Streaming commands (install, remove)

`install` and `remove` write multiple JSON lines before the terminal
`"done"` / `"error"` event. Read them asynchronously line-by-line:

```swift
/// Installs `pkg` and reports progress via the `onEvent` callback.
/// Returns the terminal `DoneData` on success or throws on failure.
@discardableResult
func installPackage(
    pkg: String,
    onEvent: @escaping (InstallEvent) -> Void
) async throws -> DoneData {
    return try await withCheckedThrowingContinuation { continuation in
        Task.detached(priority: .userInitiated) {
            let binary = brewEnginePath()
            let process = Process()
            process.executableURL = URL(fileURLWithPath: binary)
            process.arguments = ["install", pkg]
            process.environment = ProcessInfo.processInfo.environment

            let stdoutPipe = Pipe()
            process.standardOutput = stdoutPipe
            process.standardError  = Pipe()

            // Read stdout incrementally using availableData — safe for long
            // operations because we never call readDataToEndOfFile() which
            // would block until the process exits.
            let handle = stdoutPipe.fileHandleForReading
            var buffer = Data()
            var done: DoneData?

            handle.readabilityHandler = { fh in
                buffer.append(fh.availableData)
                // Extract complete lines from the buffer.
                while let range = buffer.range(of: Data("\n".utf8)) {
                    let lineData = buffer.subdata(in: buffer.startIndex..<range.lowerBound)
                    buffer.removeSubrange(buffer.startIndex...range.lowerBound)

                    guard let line = String(data: lineData, encoding: .utf8),
                          !line.trimmingCharacters(in: .whitespaces).isEmpty,
                          let raw = line.data(using: .utf8),
                          let envelope = try? JSONDecoder().decode(BrewResponse.self, from: raw)
                    else { continue }

                    switch envelope.type {
                    case "progress":
                        if let step = tryDecode(ProgressStep.self, fromRaw: raw) {
                            onEvent(.progress(step))
                        }
                    case "build_mode":
                        if let bm = tryDecode(BuildModeData.self, fromRaw: raw) {
                            onEvent(.buildMode(bm))
                        }
                    case "done":
                        if let d = tryDecode(DoneData.self, fromRaw: raw) {
                            done = d
                            onEvent(.done(d))
                        }
                    case "error":
                        if let d = tryDecode(DoneData.self, fromRaw: raw) {
                            onEvent(.failure(d, envelope.error ?? "unknown error"))
                        }
                    default:
                        break
                    }
                }
            }

            do {
                try process.run()
            } catch {
                continuation.resume(throwing: error)
                return
            }

            process.waitUntilExit()
            handle.readabilityHandler = nil

            if let d = done {
                continuation.resume(returning: d)
            } else {
                continuation.resume(throwing: BrewEngineError.processFailure(process.terminationStatus))
            }
        }
    }
}

/// Events emitted during an install or remove operation.
enum InstallEvent {
    case buildMode(BuildModeData)   // emitted at most once, early in the process
    case progress(ProgressStep)     // one per "==>" header line
    case done(DoneData)             // terminal success event
    case failure(DoneData, String)  // terminal failure event
}

// MARK: - Usage example (SwiftUI ViewModel)

// @MainActor
// func installWget() async {
//     isInstalling = true
//     steps = []
//     do {
//         let result = try await installPackage(pkg: "wget") { [weak self] event in
//             DispatchQueue.main.async {
//                 switch event {
//                 case .buildMode(let bm):
//                     self?.buildMode = bm.mode == "source" ? "Building from source…" : "Pouring bottle…"
//                 case .progress(let step):
//                     self?.steps.append(step.step)
//                 case .done, .failure:
//                     break
//                 }
//             }
//         }
//         installedVersion = result.buildMode
//     } catch {
//         errorMessage = error.localizedDescription
//     }
//     isInstalling = false
// }
```

**`remove` is identical** — just replace `"install"` with `"remove"` and
omit the `build_mode` handling if you don't need it:

```swift
func removePackage(
    pkg: String,
    onEvent: @escaping (InstallEvent) -> Void
) async throws -> DoneData {
    // Same implementation as installPackage, with args: ["remove", pkg]
    // (omitted for brevity; copy installPackage and change the argument)
    fatalError("copy installPackage and change args to [\"remove\", pkg]")
}
```

---

## 5. Watch — background mutation events

`brew-engine watch` blocks until terminated and emits `"event"` lines
whenever an external Homebrew mutation is detected (e.g. the user ran
`brew install foo` in a terminal). Use it to keep your UI in sync without
polling.

```swift
final class BrewWatcher {
    private var process: Process?
    private var task: Task<Void, Never>?

    /// Starts the watcher. `onMutation` is called on a background thread
    /// whenever an external brew change is detected; dispatch to main as needed.
    func start(onMutation: @escaping (CacheEvent) -> Void) {
        guard process == nil else { return }

        let binary = brewEnginePath()
        let p = Process()
        p.executableURL = URL(fileURLWithPath: binary)
        p.arguments = ["watch"]
        p.environment = ProcessInfo.processInfo.environment

        let pipe = Pipe()
        p.standardOutput = pipe
        p.standardError  = Pipe()

        let handle = pipe.fileHandleForReading

        task = Task.detached(priority: .background) {
            var buffer = Data()

            handle.readabilityHandler = { fh in
                buffer.append(fh.availableData)
                while let range = buffer.range(of: Data("\n".utf8)) {
                    let lineData = buffer.subdata(in: buffer.startIndex..<range.lowerBound)
                    buffer.removeSubrange(buffer.startIndex...range.lowerBound)

                    guard let line = String(data: lineData, encoding: .utf8),
                          let raw = line.data(using: .utf8),
                          let envelope = try? JSONDecoder().decode(BrewResponse.self, from: raw),
                          envelope.type == "event",
                          let event = tryDecode(CacheEvent.self, fromRaw: raw)
                    else { continue }

                    onMutation(event)
                }
            }

            do { try p.run() } catch { return }
            p.waitUntilExit()
        }

        process = p
    }

    /// Terminates the watcher process cleanly.
    func stop() {
        process?.terminate()
        process = nil
        task?.cancel()
        task = nil
    }
}

// MARK: - Usage example (AppDelegate / SwiftUI App)

// let watcher = BrewWatcher()
//
// watcher.start { event in
//     if event.action == "cache_rebuilt" {
//         DispatchQueue.main.async {
//             // Re-fetch the list to refresh the UI.
//             Task { try await viewModel.reload() }
//         }
//     }
// }
```

---

## 6. Correlation IDs (session / request)

Pass `BREW_ENGINE_SESSION_ID` and `BREW_ENGINE_REQUEST_ID` in the process
environment to correlate engine log entries with UI-side telemetry:

```swift
let sessionID = UUID().uuidString
let requestID = UUID().uuidString

let lines = try runBrewEngine(
    args: ["install", "wget"],
    env: [
        "BREW_ENGINE_SESSION_ID": sessionID,
        "BREW_ENGINE_REQUEST_ID": requestID,
    ]
)
// Every Zap log entry for this invocation will carry session_id and
// request_id fields, making cross-layer debugging trivial.
```

If your app maintains a long-lived session, set `BREW_ENGINE_SESSION_ID`
once at startup and vary only `BREW_ENGINE_REQUEST_ID` per user action.

---

## 7. Configuration via plist

brew-engine reads its configuration from:

```
~/Library/Preferences/com.mobilityquarks.brewexplorer.plist
```

Your app can write this plist at first launch to point the engine at
non-default paths, or to lock the brew binary to a specific location:

```swift
func writeBrewEnginePlist(brewPath: String? = nil, logDir: String? = nil) throws {
    let plistPath = FileManager.default.homeDirectoryForCurrentUser
        .appendingPathComponent("Library/Preferences/com.mobilityquarks.brewexplorer.plist")

    var dict: [String: Any] = [:]

    if let brewPath { dict["BrewPath"] = brewPath }
    if let logDir   { dict["LogDir"]   = logDir   }

    guard !dict.isEmpty else { return }

    let data = try PropertyListSerialization.data(
        fromPropertyList: dict,
        format: .xml,
        options: 0
    )
    try data.write(to: plistPath, options: .atomic)
}

// Example: pin brew-engine to Apple Silicon Homebrew and a custom log dir.
// try writeBrewEnginePlist(
//     brewPath: "/opt/homebrew/bin/brew",
//     logDir: "\(NSHomeDirectory())/Library/Logs/BrewExplorer"
// )
```

**Available plist keys:**

| Key | Effect | Default |
|---|---|---|
| `BrewPath` | Absolute path to the `brew` binary | Auto-detected |
| `LogDir` | Directory for `brew-engine.log` and `brew-output.log` | `~/Library/Application Support/BrewExplorer/logs` |
| `LogFileName` | File name for the structured Zap log | `brew-engine.log` |
| `BrewOutputLogFileName` | File name for raw brew output | `brew-output.log` |
| `LogLevel` | `debug` / `info` / `warn` / `error` | `info` |
| `CacheDir` | Directory for `list.json` and `info/` cache | `~/Library/Application Support/BrewExplorer/cache` |

Environment variables with the same names (prefixed `BREW_ENGINE_`) override
plist values and take highest priority.

---

## 8. Error handling patterns

### `brew_not_found` (exit code 2)

brew-engine emits this before any subcommand runs if it cannot locate `brew`:

```swift
func checkBrewAvailability() async throws {
    let lines = try await Task.detached {
        try runBrewEngine(args: ["list"])
    }.value

    for line in lines {
        guard let raw = line.data(using: .utf8),
              let envelope = try? JSONDecoder().decode(BrewResponse.self, from: raw)
        else { continue }

        if envelope.type == "brew_not_found" {
            struct NotFoundData: Decodable { let checkedPaths: [String]
                enum CodingKeys: String, CodingKey { case checkedPaths = "checked_paths" }
            }
            let data = tryDecode(NotFoundData.self, fromRaw: raw)
            throw BrewEngineError.brewNotFound(data?.checkedPaths ?? [])
        }
    }
}
```

### Generic error response

Any command can emit a `type:"error"` line. Always check before decoding
the payload:

```swift
func safeDecodeFirstLine<T: Decodable>(
    _ type: T.Type,
    from lines: [String],
    expectedType: String
) throws -> T {
    guard let firstLine = lines.first,
          let raw = firstLine.data(using: .utf8)
    else { throw BrewEngineError.processFailure(-1) }

    let envelope = try JSONDecoder().decode(BrewResponse.self, from: raw)

    if !envelope.success || envelope.type == "error" {
        throw BrewEngineError.processFailure(-1)
    }
    if envelope.type == "brew_not_found" {
        throw BrewEngineError.brewNotFound([])
    }
    return try decodePayload(type, from: firstLine, expectedType: expectedType)
}
```

---

## 9. Embedding brew-engine in the app bundle

Build the binary for the target architecture(s) and copy it into the app bundle.

```bash
# Universal binary (Intel + Apple Silicon)
GOARCH=amd64 GOFLAGS="-trimpath" go build -o /tmp/brew-engine-amd64 .
GOARCH=arm64 GOFLAGS="-trimpath" go build -o /tmp/brew-engine-arm64 .
lipo -create /tmp/brew-engine-amd64 /tmp/brew-engine-arm64 \
     -output BrewExplorer/brew-engine
```

Place it inside the app bundle so it is discoverable via
`Bundle.main.executableURL` (see `brewEnginePath()` in §2):

```
BrewExplorer.app/
└── Contents/
    ├── MacOS/
    │   ├── BrewExplorer          ← Swift app binary
    │   └── brew-engine           ← Go engine binary
    └── Info.plist
```

> **App Sandbox note:** brew-engine interacts with Homebrew directories
> outside the sandbox. Ship the app **without** the App Sandbox entitlement,
> or use a privileged helper tool (SMJobBless) if sandboxing is required.
> The binary should be code-signed with the same Developer ID as the app.
