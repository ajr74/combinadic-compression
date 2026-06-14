// Package integration drives the compiled ajz CLI end-to-end: compress then
// decompress real files and verify the round-trip and the XXH3 integrity check.
// It exercises the whole pipeline (flag parsing, file I/O, the .ajz header, the
// worker-pool + batching, and the compressor/decompressor) as a black box.
//
// Run with:
//
//	go test ./integration/
package integration

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var binPath string

func TestMain(m *testing.M) {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "getwd:", err)
		os.Exit(1)
	}
	repoRoot := filepath.Dir(wd) // integration/ -> module root

	f, err := os.CreateTemp("", "ajz-cli-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "tempfile:", err)
		os.Exit(1)
	}
	f.Close()
	binPath = f.Name()

	build := exec.Command("go", "build", "-o", binPath, ".")
	build.Dir = repoRoot
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "failed to build CLI:", err)
		os.Exit(1)
	}

	code := m.Run()
	os.Remove(binPath)
	os.Exit(code)
}

// run executes the CLI with args, returning combined stdout+stderr.
func run(args ...string) (string, error) {
	cmd := exec.Command(binPath, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dst, b)
}

// genData produces deterministic test content of the given pattern.
func genData(size int, pat string) []byte {
	b := make([]byte, size)
	rng := rand.New(rand.NewSource(int64(size)*1009 + int64(len(pat))))
	switch pat {
	case "zeros":
		// already all zero
	case "single":
		for i := range b {
			b[i] = 0x41
		}
	case "text":
		const alpha = "the quick brown fox jumps over the lazy dog 0123456789 .,\n"
		for i := range b {
			b[i] = alpha[rng.Intn(len(alpha))]
		}
	default: // "random": full-range binary, includes byte 0x00 and 0xFF
		rng.Read(b)
	}
	return b
}

// roundTrip compresses data and decompresses it through the CLI, asserting the
// XXH3 check passes and the bytes are recovered exactly. Decompression happens
// in a fresh directory so the original is never clobbered.
func roundTrip(t *testing.T, data []byte, block, jobs int) {
	t.Helper()
	in := filepath.Join(t.TempDir(), "input")
	writeFile(t, in, data)

	out, err := run("-k", "-b", strconv.Itoa(block), "-j", strconv.Itoa(jobs), in)
	if err != nil {
		t.Fatalf("compress exited with error: %v\n%s", err, out)
	}
	if !fileExists(in + ".ajz") {
		t.Fatalf("no .ajz produced; output:\n%s", out)
	}

	decDir := t.TempDir()
	ajz := filepath.Join(decDir, "input.ajz")
	copyFile(t, in+".ajz", ajz)

	out, err = run("-d", "-k", "-j", strconv.Itoa(jobs), ajz)
	if err != nil {
		t.Fatalf("decompress exited with error: %v\n%s", err, out)
	}
	if !strings.Contains(out, "hashes agree") {
		t.Fatalf("XXH3 check did not pass; output:\n%s", out)
	}

	got, err := os.ReadFile(filepath.Join(decDir, "input"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("round-trip mismatch: got %d bytes, want %d", len(got), len(data))
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func TestRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI integration test in -short mode")
	}
	cases := []struct {
		name              string
		size, block, jobs int
		pat               string
	}{
		{"text_small", 1000, 1024, 4, "text"},
		{"text_large", 200000, 1024, 8, "text"},
		{"random_binary", 50000, 1024, 4, "random"}, // includes byte 0xFF (regression)
		{"zeros", 8192, 1024, 2, "zeros"},
		{"single_value", 5000, 1024, 4, "single"},
		{"block_64", 2000, 64, 4, "text"}, // < 256: regression for the cache-sizing fix
		{"block_128", 2000, 128, 4, "random"},
		{"block_256", 2000, 256, 4, "text"},
		{"block_4096", 20000, 4096, 4, "text"},
		{"exact_multiple", 4096, 1024, 4, "random"}, // size = 4*block, remainder 0
		{"block_plus_one", 1025, 1024, 1, "random"}, // remainder = 1
		{"smaller_than_block", 400, 1024, 4, "random"},
		{"single_thread", 30000, 1024, 1, "random"},
		{"many_jobs", 30000, 1024, 16, "random"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			roundTrip(t, genData(c.size, c.pat), c.block, c.jobs)
		})
	}
}

// TestCorruptedHashDetected flips a byte of the trailing XXH3 hash; the data
// still decompresses but the integrity check must report a mismatch.
func TestCorruptedHashDetected(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI integration test in -short mode")
	}
	in := filepath.Join(t.TempDir(), "input")
	writeFile(t, in, genData(20000, "random"))
	if out, err := run("-k", "-b", "1024", "-j", "4", in); err != nil {
		t.Fatalf("compress failed: %v\n%s", err, out)
	}

	ajz := filepath.Join(t.TempDir(), "input.ajz")
	copyFile(t, in+".ajz", ajz)
	b, err := os.ReadFile(ajz)
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)-1] ^= 0xFF // corrupt the stored hash (last 8 bytes)
	writeFile(t, ajz, b)

	out, err := run("-d", "-k", "-j", "4", ajz)
	if err != nil {
		t.Fatalf("decompress exited with error: %v\n%s", err, out)
	}
	if strings.Contains(out, "hashes agree") || !strings.Contains(out, "disagree") {
		t.Fatalf("expected a hash-mismatch report; output:\n%s", out)
	}
}

// TestWrongSuffixRejected verifies the CLI refuses to decompress a file that does
// not end in .ajz.
func TestWrongSuffixRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI integration test in -short mode")
	}
	notAjz := filepath.Join(t.TempDir(), "plain.bin")
	writeFile(t, notAjz, genData(100, "random"))
	out, _ := run("-d", notAjz)
	if !strings.Contains(out, "must end with") {
		t.Fatalf("expected a suffix-rejection message; output:\n%s", out)
	}
}
