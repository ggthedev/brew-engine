package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestBinarySize builds the brew-engine binary in a temp directory, reports
// its size, and fails if it exceeds the defined budget. This test acts as a
// compile-time size regression guard for CI/CD pipelines.
//
// Size budget rationale:
//   - A stripped Go binary with Cobra + Zap is typically 12–18 MB.
//   - The 30 MB ceiling leaves headroom for future dependencies while
//     still catching accidental inclusion of large vendored assets.
func TestBinarySize(t *testing.T) {
	const maxBytes = 30 * 1024 * 1024 // 30 MB ceiling

	binPath := filepath.Join(t.TempDir(), "brew-engine-size-check")

	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = "." // package main → module root is the working directory
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	info, err := os.Stat(binPath)
	if err != nil {
		t.Fatalf("cannot stat binary: %v", err)
	}

	sizeBytes := info.Size()
	sizeMB := float64(sizeBytes) / (1024 * 1024)

	t.Logf("Binary size: %d bytes (%.2f MB)", sizeBytes, sizeMB)

	if sizeBytes > maxBytes {
		t.Errorf("binary size %.2f MB exceeds budget of %.2f MB",
			sizeMB, float64(maxBytes)/(1024*1024))
	}
}

// TestBinarySize_Stripped builds with ldflags to strip debug symbols and
// reports the resulting size. This mirrors a production release build and
// is the most accurate measure of distribution size.
func TestBinarySize_Stripped(t *testing.T) {
	const maxBytes = 20 * 1024 * 1024 // 20 MB ceiling for stripped binary

	binPath := filepath.Join(t.TempDir(), "brew-engine-stripped")

	cmd := exec.Command("go", "build",
		"-ldflags", "-s -w",
		"-o", binPath,
		".",
	)
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("stripped build failed: %v\n%s", err, out)
	}

	info, err := os.Stat(binPath)
	if err != nil {
		t.Fatalf("cannot stat stripped binary: %v", err)
	}

	sizeBytes := info.Size()
	sizeMB := float64(sizeBytes) / (1024 * 1024)

	t.Logf("Stripped binary size: %d bytes (%.2f MB)", sizeBytes, sizeMB)

	if sizeBytes > maxBytes {
		t.Errorf("stripped binary %.2f MB exceeds budget of %.2f MB",
			sizeMB, float64(maxBytes)/(1024*1024))
	}
}

// BenchmarkBuild measures the incremental build time of the module. This
// surfaces regressions in compile time caused by adding large dependencies.
// Run with: go test -bench=BenchmarkBuild -benchtime=3x -run=^$ .
func BenchmarkBuild(b *testing.B) {
	for i := 0; i < b.N; i++ {
		binPath := filepath.Join(b.TempDir(), fmt.Sprintf("brew-engine-bench-%d", i))
		cmd := exec.Command("go", "build", "-o", binPath, ".")
		cmd.Dir = "."
		if err := cmd.Run(); err != nil {
			b.Fatal(err)
		}
		info, _ := os.Stat(binPath)
		b.ReportMetric(float64(info.Size()), "bytes/binary")
	}
}
