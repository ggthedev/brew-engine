// Package contract defines the strict JSON communication schema between
// brew-engine and any frontend (SwiftUI or Bubble Tea TUI).
// Every byte written to stdout MUST be a marshaled Response.
package contract

import (
	"encoding/json"
	"fmt"
	"io"
)

// ─── Envelope ────────────────────────────────────────────────────────────────

// Response is the top-level envelope for every JSON line written to stdout.
// Type disambiguates the shape of Data for the consumer.
type Response struct {
	Success bool        `json:"success"`
	Type    string      `json:"type"`            // "list" | "info" | "progress" | "done" | "error"
	Error   string      `json:"error,omitempty"` // populated only when Success is false
	Data    interface{} `json:"data,omitempty"`
}

// WriteJSON marshals r and writes it as a single newline-terminated JSON line
// to w. On marshal failure it writes a safe fallback error payload.
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
// Fields are deliberately minimal; only what the UI needs is included.
type FormulaInfo struct {
	Name             string `json:"name"`
	FullName         string `json:"full_name"`
	Tap              string `json:"tap"`
	Description      string `json:"desc"`
	Homepage         string `json:"homepage"`
	Version          string `json:"version"`
	InstalledVersion string `json:"installed_version,omitempty"`
	Installed        bool   `json:"installed"`
	Outdated         bool   `json:"outdated"`
	Pinned           bool   `json:"pinned"`
}

// CaskInfo is the clean, UI-friendly projection of a Homebrew cask.
type CaskInfo struct {
	Token       string `json:"token"`
	FullToken   string `json:"full_token"`
	Tap         string `json:"tap"`
	Name        string `json:"name"`
	Description string `json:"desc"`
	Homepage    string `json:"homepage"`
	Version     string `json:"version"`
	Installed   bool   `json:"installed"`
	Outdated    bool   `json:"outdated"`
}

// ListData is the payload for a Type="list" response.
type ListData struct {
	Formulae []FormulaInfo `json:"formulae"`
	Casks    []CaskInfo    `json:"casks"`
	Total    int           `json:"total"`
}

// ─── Raw Homebrew JSON v2 types (unmarshal targets, never sent to the UI) ────

// BrewInfoV2 is the top-level shape returned by `brew info --json=v2`.
type BrewInfoV2 struct {
	Formulae []RawFormula `json:"formulae"`
	Casks    []RawCask    `json:"casks"`
}

// RawFormula captures the fields we care about from Homebrew's formula schema.
type RawFormula struct {
	Name      string         `json:"name"`
	FullName  string         `json:"full_name"`
	Tap       string         `json:"tap"`
	Desc      string         `json:"desc"`
	Homepage  string         `json:"homepage"`
	Versions  RawVersions    `json:"versions"`
	Installed []RawInstalled `json:"installed"`
	Pinned    bool           `json:"pinned"`
	Outdated  bool           `json:"outdated"`
}

// RawVersions holds the version block from Homebrew's formula JSON v2.
type RawVersions struct {
	Stable string `json:"stable"`
	Head   string `json:"head"`
	Bottle bool   `json:"bottle"`
}

// RawInstalled represents a single installed version entry.
type RawInstalled struct {
	Version string `json:"version"`
}

// RawCask captures the fields we care about from Homebrew's cask schema.
// Installed is a version string when installed, or null (empty string after
// unmarshal) when not installed.
type RawCask struct {
	Token     string   `json:"token"`
	FullToken string   `json:"full_token"`
	Tap       string   `json:"tap"`
	Name      []string `json:"name"`
	Desc      string   `json:"desc"`
	Homepage  string   `json:"homepage"`
	Version   string   `json:"version"`
	Installed string   `json:"installed"` // empty string == not installed
	Outdated  bool     `json:"outdated"`
}

// ─── State-changing command types (install / remove) ─────────────────────────

// ProgressStep is emitted (Type="progress") for every `==>` line intercepted
// from a running brew install/remove process.
type ProgressStep struct {
	Package string `json:"package"`
	Step    string `json:"step"`
}

// DoneData is the final payload emitted (Type="done") when a state-changing
// command exits, regardless of success or failure.
type DoneData struct {
	Package  string `json:"package"`
	ExitCode int    `json:"exit_code"`
}
