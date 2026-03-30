// Package contract defines the strict, versioned JSON communication schema
// shared between brew-engine and every frontend layer.
//
// # Design contract
//
// Every single line written to stdout by brew-engine MUST be a complete,
// newline-terminated JSON object that unmarshals cleanly into [Response].
// This guarantee holds even for fatal errors — no raw text, no partial
// writes, no panics that produce non-JSON output.
//
// # Type taxonomy
//
// The package is divided into three groups:
//
//   - Envelope: [Response] and [WriteJSON] — the wire format.
//   - UI types: [FormulaInfo], [CaskInfo], [ListData] — clean projections
//     sent to the frontend after filtering Homebrew's verbose JSON.
//   - Raw types: [BrewInfoV2], [RawFormula], [RawCask], [RawVersions],
//     [RawInstalled] — unmarshal targets for brew's native JSON v2 output;
//     these are internal and never forwarded to the frontend.
//   - Streaming types: [ProgressStep], [DoneData] — payloads for the
//     incremental install/remove event stream.
package contract

import (
	"encoding/json"
	"fmt"
	"io"
)

// ─── Envelope ────────────────────────────────────────────────────────────────

// Response is the top-level envelope for every JSON line written to stdout.
// Consumers must inspect [Response.Type] to determine the concrete type
// that [Response.Data] should be decoded into.
//
// Valid Type values:
//
//   - "list"     — Data is [NamesList]; emitted by the list subcommand.
//   - "info"     — Data is [FormulaInfo] or [CaskInfo]; emitted by info.
//   - "progress" — Data is [ProgressStep]; emitted once per "==>" line
//     during an install or remove operation.
//   - "done"     — Data is [DoneData]; final event on successful exit.
//   - "error"    — Data is [DoneData] (when exit code is available) or
//     omitted; Success is always false for this type.
//   - "event"    — Data is [CacheEvent]; emitted by the cache watcher
//     when a background rebuild completes.
//   - "build_mode" — Data is [BuildModeData]; emitted once per install
//     operation, as soon as the parser determines whether Homebrew is
//     pouring a pre-compiled bottle or compiling from source.
//   - "brew_not_found" — Data is [BrewNotFoundData]; emitted by main before
//     any subcommand runs when Homebrew cannot be located on disk.
//     The process exits with code 2 immediately after this event.
//   - "nuke"          — Data is [NukeData]; emitted by the nuke subcommand
//     after running brew cleanup and/or wiping the app cache.
type Response struct {
	// Success indicates whether the underlying brew operation succeeded.
	// When false, Error is guaranteed to be non-empty.
	Success bool `json:"success"`

	// Type identifies the shape of the Data field. See the list of valid
	// values in the type-level documentation.
	Type string `json:"type"` // "list" | "info" | "progress" | "done" | "error" | "event" | "build_mode" | "brew_not_found" | "nuke"

	// IsStale is true when the response was served from an expired cache
	// entry. A fresh response will follow on stdout once the background
	// revalidation goroutine completes. Omitted from JSON when false.
	IsStale bool `json:"is_stale,omitempty"`

	// Error is a human-readable description of the failure. It is only
	// present when Success is false; omitted from JSON when empty.
	Error string `json:"error,omitempty"`

	// Data carries the command-specific payload. Its concrete type is
	// determined by the Type field. Omitted from JSON when nil.
	Data interface{} `json:"data,omitempty"`
}

// WriteJSON marshals r into JSON and writes it as a single newline-terminated
// line to w. It is the only sanctioned way to produce output from brew-engine;
// all subcommands and the parser must use this function exclusively.
//
// If json.Marshal fails (which should never happen given the types involved),
// WriteJSON writes a hard-coded fallback error JSON line so that the caller
// always receives valid JSON regardless of internal marshaling issues.
//
// WriteJSON is safe to call from multiple goroutines only when w's underlying
// Write implementation is atomic for small payloads (e.g. os.Stdout on macOS,
// where writes < PIPE_BUF are atomic per POSIX). The parser relies on this
// property when draining stdout and stderr concurrently.
func WriteJSON(w io.Writer, r Response) {
	b, err := json.Marshal(r)
	if err != nil {
		fmt.Fprintf(w, "{\"success\":false,\"type\":\"error\",\"error\":\"json marshal failed: %s\"}\n", err.Error())
		return
	}
	fmt.Fprintln(w, string(b))
}

// ─── Read-only command types (list / info) ───────────────────────────────────

// FormulaInfo is the clean, UI-friendly projection of a Homebrew formula.
// It is derived from [RawFormula] by retaining only the fields required for
// display in a package browser; the full Homebrew JSON v2 payload (which can
// be several kilobytes per formula) is never forwarded to the frontend.
type FormulaInfo struct {
	// Name is the short formula identifier (e.g. "wget").
	Name string `json:"name"`

	// FullName includes the tap prefix when the formula is not in the default
	// homebrew/core tap (e.g. "hashicorp/tap/terraform").
	FullName string `json:"full_name"`

	// Tap is the source repository for the formula (e.g. "homebrew/core").
	Tap string `json:"tap"`

	// Description is the one-line summary from the formula's Ruby definition.
	Description string `json:"desc"`

	// Homepage is the upstream project URL.
	Homepage string `json:"homepage"`

	// Version is the latest stable version available in the tap.
	Version string `json:"version"`

	// InstalledVersion is the version currently installed on the system.
	// Omitted from JSON when the formula is not installed.
	InstalledVersion string `json:"installed_version,omitempty"`

	// Installed is true when at least one version of the formula is present
	// under the Homebrew Cellar.
	Installed bool `json:"installed"`

	// Outdated is true when the installed version is older than the latest
	// stable version available in the tap.
	Outdated bool `json:"outdated"`

	// Pinned is true when the formula has been pinned with `brew pin` and
	// will not be upgraded by `brew upgrade`.
	Pinned bool `json:"pinned"`
}

// CaskInfo is the clean, UI-friendly projection of a Homebrew cask.
// Casks represent macOS GUI applications and other binary distributions
// that Homebrew installs into /Applications or ~/Applications.
type CaskInfo struct {
	// Token is the short cask identifier used on the command line (e.g. "firefox").
	Token string `json:"token"`

	// FullToken includes the tap prefix for non-default-tap casks.
	FullToken string `json:"full_token"`

	// Tap is the source repository for the cask (e.g. "homebrew/cask").
	Tap string `json:"tap"`

	// Name is the human-readable display name (e.g. "Mozilla Firefox").
	// Derived from the first element of the raw name array.
	Name string `json:"name"`

	// Description is the one-line summary from the cask's Ruby definition.
	Description string `json:"desc"`

	// Homepage is the upstream project URL.
	Homepage string `json:"homepage"`

	// Version is the latest version string available in the tap.
	Version string `json:"version"`

	// Installed is true when the cask is currently installed on the system.
	Installed bool `json:"installed"`

	// Outdated is true when the installed version differs from the latest
	// available version in the tap.
	Outdated bool `json:"outdated"`
}

// ListData is the payload carried by a [Response] with Type="list".
// It contains all installed formulae and casks as clean [FormulaInfo] and
// [CaskInfo] projections, along with an aggregate total count.
type ListData struct {
	// Formulae holds the projected data for every installed formula.
	Formulae []FormulaInfo `json:"formulae"`

	// Casks holds the projected data for every installed cask.
	Casks []CaskInfo `json:"casks"`

	// Total is len(Formulae) + len(Casks), provided as a convenience field
	// so the frontend does not need to recompute it.
	Total int `json:"total"`
}

// NamesList is the lightweight payload for the `list` command. It contains
// only the bare names (formula identifiers and cask tokens) of every
// installed package, with no version, description, or status fields.
//
// This is the default output of `brew-engine list`. It is derived from
// `brew list --formula` and `brew list --cask`, which are significantly
// faster than `brew info --installed --json=v2` because they do not
// resolve dependency graphs or fetch remote metadata.
type NamesList struct {
	// Formulae is the sorted list of installed formula names
	// (e.g. ["git", "wget", "zsh"]).
	Formulae []string `json:"formulae"`

	// Casks is the sorted list of installed cask tokens
	// (e.g. ["firefox", "iterm2"]).
	Casks []string `json:"casks"`

	// Total is len(Formulae) + len(Casks).
	Total int `json:"total"`
}

// ─── Raw Homebrew JSON v2 types (unmarshal targets, never sent to the UI) ────

// BrewInfoV2 is the top-level unmarshal target for the JSON payload returned
// by `brew info --json=v2`. It is an internal type — the fields here are a
// strict subset of Homebrew's full schema; unknown fields are silently ignored
// by encoding/json. This type is NEVER serialised and sent to the frontend.
type BrewInfoV2 struct {
	// Formulae contains the formula entries returned by brew.
	Formulae []RawFormula `json:"formulae"`

	// Casks contains the cask entries returned by brew.
	Casks []RawCask `json:"casks"`
}

// RawFormula is a partial unmarshal target for a single formula object
// inside Homebrew's JSON v2 response. Only the fields consumed by the
// projection step are declared; all others are discarded by the decoder.
type RawFormula struct {
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Tap      string `json:"tap"`
	Desc     string `json:"desc"`
	Homepage string `json:"homepage"`
	// Versions holds the available version strings for this formula.
	Versions RawVersions `json:"versions"`
	// Installed is a slice because multiple versions can coexist in the Cellar.
	// An empty slice means the formula is not currently installed.
	Installed []RawInstalled `json:"installed"`
	Pinned    bool           `json:"pinned"`
	Outdated  bool           `json:"outdated"`
}

// RawVersions holds the version block from Homebrew's formula JSON v2.
// Only Stable is used in the UI projection; Head and Bottle are retained
// so that the struct faithfully reflects the upstream schema.
type RawVersions struct {
	// Stable is the latest stable release version string (e.g. "1.25.0").
	Stable string `json:"stable"`

	// Head is the version string for the HEAD/bleeding-edge revision, if any.
	Head string `json:"head"`

	// Bottle indicates whether a pre-compiled bottle exists for this version.
	Bottle bool `json:"bottle"`
}

// RawInstalled represents a single installed version entry within the Cellar.
// The Homebrew JSON schema embeds more fields (runtime_dependencies, options,
// etc.) which are intentionally omitted here as they are not required by the UI.
type RawInstalled struct {
	// Version is the version string of this particular Cellar installation.
	Version string `json:"version"`
}

// RawCask is a partial unmarshal target for a single cask object inside
// Homebrew's JSON v2 response. Only the fields consumed by the projection
// step are declared.
type RawCask struct {
	Token     string `json:"token"`
	FullToken string `json:"full_token"`
	Tap       string `json:"tap"`
	// Name is a slice because some casks carry multiple display names;
	// the projection uses Name[0] when available, falling back to Token.
	Name     []string `json:"name"`
	Desc     string   `json:"desc"`
	Homepage string   `json:"homepage"`
	Version  string   `json:"version"`
	// Installed holds the currently installed version string, or an empty
	// string when the cask is not installed. The upstream JSON value is
	// null when not installed, which encoding/json decodes to "".
	Installed string `json:"installed"`
	Outdated  bool   `json:"outdated"`
}

// ─── Cache event types ───────────────────────────────────────────────────────

// CacheEvent is the Data payload carried by a [Response] with Type="event".
// It is emitted by the cache watcher goroutine whenever a background cache
// rebuild completes, signalling the frontend to refresh its view.
type CacheEvent struct {
	// Action describes what happened (e.g. "cache_rebuilt").
	Action string `json:"action"`

	// Target names the cache artefact that was rebuilt (e.g. "list").
	Target string `json:"target"`
}

// ─── State-changing command types (install / remove) ─────────────────────────

// BuildModeData is the Data payload carried by a [Response] with
// Type="build_mode". It is emitted exactly once per install operation,
// as soon as the parser can determine from the brew output stream whether
// Homebrew is pouring a pre-compiled bottle or building from source.
//
// Mode values:
//
//   - "bottle" — Homebrew is pouring a cached, pre-compiled binary.
//     Expected duration: a few seconds.
//   - "source" — No bottle is available; Homebrew will compile the
//     formula (and possibly its dependencies) from source.
//     Expected duration: minutes to tens of minutes.
//
// The frontend should use this event to adjust its progress UI:
// a spinner is appropriate for "bottle"; a progress bar with a
// "this may take several minutes" warning suits "source".
type BuildModeData struct {
	// Package is the name of the formula being installed.
	Package string `json:"package"`

	// Mode is either "bottle" or "source".
	Mode string `json:"mode"`
}

// ProgressStep is the Data payload carried by a [Response] with
// Type="progress". One ProgressStep is emitted for every line that begins
// with "==>" in the brew subprocess output, giving the frontend a
// structured, ANSI-free view of each installation phase as it happens.
type ProgressStep struct {
	// Package is the name of the formula or cask being operated on.
	Package string `json:"package"`

	// Step is the full text of the "==>" header line, stripped of all ANSI
	// escape sequences (e.g. "==> Downloading https://...").
	Step string `json:"step"`
}

// BrewNotFoundData is the Data payload carried by a [Response] with
// Type="brew_not_found". It is emitted by main() before logger or subcommand
// initialisation when Homebrew cannot be located at any standard path.
//
// The frontend should present an actionable error: e.g. a link to
// https://brew.sh or instructions to set the BrewPath plist key.
type BrewNotFoundData struct {
	// CheckedPaths lists the well-known locations that were probed, in
	// priority order, before giving up. Useful for diagnostics.
	CheckedPaths []string `json:"checked_paths"`
}

// NukeData is the Data payload carried by a [Response] with Type="nuke".
// It is emitted by the nuke subcommand after running brew cleanup and/or
// wiping the app cache directory.
type NukeData struct {
	// BrewCacheNuked is true when `brew cleanup -s --prune=all` was executed.
	BrewCacheNuked bool `json:"brew_cache_nuked,omitempty"`

	// BrewExitCode is the exit code returned by the brew cleanup subprocess.
	// Present only when BrewCacheNuked is true.
	BrewExitCode int `json:"brew_exit_code,omitempty"`

	// AppCacheNuked is true when the app cache directory was wiped.
	AppCacheNuked bool `json:"app_cache_nuked,omitempty"`

	// AppCacheDir is the absolute path of the app cache that was wiped.
	// Present only when AppCacheNuked is true.
	AppCacheDir string `json:"app_cache_dir,omitempty"`
}

// DoneData is the final Data payload emitted after a state-changing brew
// command (install or remove) terminates. It is present in both the
// Type="done" (success) and Type="error" (failure) terminal events, allowing
// the frontend to always know which package was involved and the exact exit
// status reported by the brew subprocess.
type DoneData struct {
	// Package is the name of the formula or cask that was operated on.
	Package string `json:"package"`

	// ExitCode is the OS-level exit code of the brew subprocess.
	// 0 indicates success; any other value indicates failure.
	ExitCode int `json:"exit_code"`

	// BuildMode records how the package was installed: "bottle" for a
	// pre-compiled binary or "source" for a from-source compilation.
	// Omitted when empty (e.g. for remove operations or when the mode
	// could not be determined before the process exited).
	BuildMode string `json:"build_mode,omitempty"`
}
