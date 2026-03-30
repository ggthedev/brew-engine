package contract

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// ── WriteJSON ────────────────────────────────────────────────────────────────

func TestWriteJSON_Success(t *testing.T) {
	var buf bytes.Buffer
	WriteJSON(&buf, Response{Success: true, Type: "list"})

	line := strings.TrimSuffix(buf.String(), "\n")
	var got Response
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if !got.Success || got.Type != "list" {
		t.Errorf("unexpected response: %+v", got)
	}
}

func TestWriteJSON_NewlineTerminated(t *testing.T) {
	var buf bytes.Buffer
	WriteJSON(&buf, Response{Success: true, Type: "done"})
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Error("output must be newline-terminated")
	}
}

func TestWriteJSON_MarshalFailure_FallbackIsValidJSON(t *testing.T) {
	var buf bytes.Buffer
	// channels cannot be JSON-marshalled, triggering the WriteJSON fallback path
	WriteJSON(&buf, Response{Success: true, Type: "test", Data: make(chan int)})

	out := strings.TrimSuffix(buf.String(), "\n")
	var r Response
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("fallback output must still be valid JSON: %v", err)
	}
	if r.Success {
		t.Error("fallback response must have success=false")
	}
	if r.Error == "" {
		t.Error("fallback response must have a non-empty error field")
	}
}

// ── Response struct ──────────────────────────────────────────────────────────

func TestResponse_OmitEmpty_ErrorField(t *testing.T) {
	b, _ := json.Marshal(Response{Success: true, Type: "done"})
	if strings.Contains(string(b), `"error"`) {
		t.Errorf("empty Error field must be omitted from JSON: %s", b)
	}
}

func TestResponse_OmitEmpty_DataField(t *testing.T) {
	b, _ := json.Marshal(Response{Success: true, Type: "done"})
	if strings.Contains(string(b), `"data"`) {
		t.Errorf("nil Data field must be omitted from JSON: %s", b)
	}
}

func TestResponse_ErrorPresent(t *testing.T) {
	b, _ := json.Marshal(Response{Success: false, Type: "error", Error: "oops"})
	if !strings.Contains(string(b), `"error":"oops"`) {
		t.Errorf("error field must appear when non-empty: %s", b)
	}
}

// ── NamesList ────────────────────────────────────────────────────────────────

func TestNamesList_RoundTrip(t *testing.T) {
	orig := NamesList{
		Formulae: []string{"git", "wget"},
		Casks:    []string{"firefox"},
		Total:    3,
	}
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatal(err)
	}
	var got NamesList
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Total != 3 || len(got.Formulae) != 2 || len(got.Casks) != 1 {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

// ── FormulaInfo ──────────────────────────────────────────────────────────────

func TestFormulaInfo_RoundTrip(t *testing.T) {
	orig := FormulaInfo{
		Name:             "wget",
		FullName:         "wget",
		Tap:              "homebrew/core",
		Description:      "Internet file retriever",
		Homepage:         "https://www.gnu.org/software/wget/",
		Version:          "1.25.0",
		InstalledVersion: "1.25.0",
		Installed:        true,
		Outdated:         false,
		Pinned:           false,
	}
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatal(err)
	}
	var got FormulaInfo
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "wget" || !got.Installed || got.InstalledVersion != "1.25.0" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestFormulaInfo_InstalledVersionOmitEmpty(t *testing.T) {
	b, _ := json.Marshal(FormulaInfo{Name: "wget", Installed: false})
	if strings.Contains(string(b), `"installed_version"`) {
		t.Errorf("InstalledVersion must be omitted when empty: %s", b)
	}
}

// ── CaskInfo ─────────────────────────────────────────────────────────────────

func TestCaskInfo_RoundTrip(t *testing.T) {
	orig := CaskInfo{
		Token:       "firefox",
		FullToken:   "firefox",
		Tap:         "homebrew/cask",
		Name:        "Mozilla Firefox",
		Description: "Web browser",
		Homepage:    "https://www.mozilla.org/",
		Version:     "120.0",
		Installed:   true,
		Outdated:    false,
	}
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatal(err)
	}
	var got CaskInfo
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Token != "firefox" || !got.Installed || got.Name != "Mozilla Firefox" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

// ── ListData ─────────────────────────────────────────────────────────────────

func TestListData_RoundTrip(t *testing.T) {
	orig := ListData{
		Formulae: []FormulaInfo{{Name: "git", Installed: true}},
		Casks:    []CaskInfo{{Token: "firefox", Installed: true}},
		Total:    2,
	}
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatal(err)
	}
	var got ListData
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Total != 2 || len(got.Formulae) != 1 || len(got.Casks) != 1 {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

// ── Raw Homebrew types ───────────────────────────────────────────────────────

func TestBrewInfoV2_RoundTrip(t *testing.T) {
	orig := BrewInfoV2{
		Formulae: []RawFormula{
			{
				Name:      "wget",
				FullName:  "wget",
				Tap:       "homebrew/core",
				Desc:      "Internet file retriever",
				Homepage:  "https://gnu.org",
				Versions:  RawVersions{Stable: "1.25.0", Head: "HEAD", Bottle: true},
				Installed: []RawInstalled{{Version: "1.25.0"}},
				Pinned:    false,
				Outdated:  false,
			},
		},
		Casks: []RawCask{
			{
				Token:     "firefox",
				FullToken: "firefox",
				Tap:       "homebrew/cask",
				Name:      []string{"Mozilla Firefox"},
				Desc:      "Web browser",
				Version:   "120.0",
				Installed: "120.0",
				Outdated:  false,
			},
		},
	}
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatal(err)
	}
	var got BrewInfoV2
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Formulae) != 1 || got.Formulae[0].Name != "wget" {
		t.Errorf("formulae round-trip failed: %+v", got.Formulae)
	}
	if len(got.Casks) != 1 || got.Casks[0].Token != "firefox" {
		t.Errorf("casks round-trip failed: %+v", got.Casks)
	}
}

func TestRawVersions_RoundTrip(t *testing.T) {
	orig := RawVersions{Stable: "1.0", Head: "HEAD", Bottle: true}
	b, _ := json.Marshal(orig)
	var got RawVersions
	json.Unmarshal(b, &got)
	if got.Stable != "1.0" || !got.Bottle {
		t.Errorf("round-trip failed: %+v", got)
	}
}

func TestRawInstalled_RoundTrip(t *testing.T) {
	orig := RawInstalled{Version: "2.0"}
	b, _ := json.Marshal(orig)
	var got RawInstalled
	json.Unmarshal(b, &got)
	if got.Version != "2.0" {
		t.Errorf("round-trip failed: %+v", got)
	}
}

func TestRawCask_NullInstalled_DecodesToEmptyString(t *testing.T) {
	raw := `{"token":"vlc","full_token":"vlc","tap":"homebrew/cask","name":["VLC"],` +
		`"desc":"","homepage":"","version":"3.0","installed":null,"outdated":false}`
	var rc RawCask
	if err := json.Unmarshal([]byte(raw), &rc); err != nil {
		t.Fatal(err)
	}
	if rc.Installed != "" {
		t.Errorf("JSON null in installed field must decode to empty string, got %q", rc.Installed)
	}
}

// ── Streaming types ──────────────────────────────────────────────────────────

func TestProgressStep_RoundTrip(t *testing.T) {
	orig := ProgressStep{Package: "wget", Step: "==> Downloading https://example.com"}
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatal(err)
	}
	var got ProgressStep
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Package != "wget" || got.Step != "==> Downloading https://example.com" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestDoneData_RoundTrip(t *testing.T) {
	orig := DoneData{Package: "wget", ExitCode: 0}
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatal(err)
	}
	var got DoneData
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Package != "wget" || got.ExitCode != 0 {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestDoneData_NonZeroExitCode(t *testing.T) {
	orig := DoneData{Package: "wget", ExitCode: 1}
	b, _ := json.Marshal(orig)
	var got DoneData
	json.Unmarshal(b, &got)
	if got.ExitCode != 1 {
		t.Errorf("expected exit_code 1, got %d", got.ExitCode)
	}
}
