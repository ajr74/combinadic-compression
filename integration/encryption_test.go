package integration

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// roundTripEncrypted compresses with -enc <mode> -keyfile and decompresses with
// the same key, asserting the XXH3 check passes and the bytes round-trip exactly.
func roundTripEncrypted(t *testing.T, data []byte, block int, mode string) {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "input")
	writeFile(t, in, data)
	key := filepath.Join(dir, "key.bin")
	writeFile(t, key, []byte("an-example-master-key-0123456789"))

	out, err := run("-k", "-b", strconv.Itoa(block), "-j", "4", "-enc", mode, "-keyfile", key, in)
	if err != nil {
		t.Fatalf("encrypted compress failed (%s): %v\n%s", mode, err, out)
	}
	if !fileExists(in + ".ajz") {
		t.Fatalf("no .ajz produced (%s); output:\n%s", mode, out)
	}

	decDir := t.TempDir()
	ajz := filepath.Join(decDir, "input.ajz")
	copyFile(t, in+".ajz", ajz)

	out, err = run("-d", "-k", "-j", "4", "-keyfile", key, ajz)
	if err != nil {
		t.Fatalf("encrypted decompress failed (%s): %v\n%s", mode, err, out)
	}
	if !strings.Contains(out, "hashes agree") {
		t.Fatalf("XXH3 check did not pass (%s); output:\n%s", mode, out)
	}
	got, err := os.ReadFile(filepath.Join(decDir, "input"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("encrypted round-trip mismatch (%s): got %d bytes, want %d", mode, len(got), len(data))
	}
}

func TestEncryptedRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI integration test in -short mode")
	}
	for _, mode := range []string{"full", "query"} {
		t.Run(mode+"/text", func(t *testing.T) {
			roundTripEncrypted(t, genData(50000, "text"), 1024, mode)
		})
		t.Run(mode+"/random", func(t *testing.T) {
			roundTripEncrypted(t, genData(50000, "random"), 1024, mode)
		})
	}
}

// TestEncryptedWrongKeyFails compresses with one key and decompresses with
// another: the round-trip must not report agreement (it fails the XXH3 check or
// exits with an error), and must not recover the plaintext.
func TestEncryptedWrongKeyFails(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI integration test in -short mode")
	}
	dir := t.TempDir()
	in := filepath.Join(dir, "input")
	data := genData(40000, "text")
	writeFile(t, in, data)
	keyA := filepath.Join(dir, "keyA.bin")
	keyB := filepath.Join(dir, "keyB.bin")
	writeFile(t, keyA, []byte("master-key-AAAAAAAAAAAAAAAAAAAAAA"))
	writeFile(t, keyB, []byte("master-key-BBBBBBBBBBBBBBBBBBBBBB"))

	if out, err := run("-k", "-b", "1024", "-j", "4", "-enc", "full", "-keyfile", keyA, in); err != nil {
		t.Fatalf("compress failed: %v\n%s", err, out)
	}
	decDir := t.TempDir()
	ajz := filepath.Join(decDir, "input.ajz")
	copyFile(t, in+".ajz", ajz)

	out, _ := run("-d", "-k", "-j", "4", "-keyfile", keyB, ajz) // wrong key; may exit nonzero
	if strings.Contains(out, "hashes agree") {
		t.Fatalf("wrong key reported success; output:\n%s", out)
	}
	if got, err := os.ReadFile(filepath.Join(decDir, "input")); err == nil && bytes.Equal(got, data) {
		t.Fatalf("wrong key recovered the plaintext")
	}
}

// TestEncryptedRequiresKey verifies that decompressing an encrypted file without
// a key is refused.
func TestEncryptedRequiresKey(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI integration test in -short mode")
	}
	dir := t.TempDir()
	in := filepath.Join(dir, "input")
	writeFile(t, in, genData(10000, "text"))
	key := filepath.Join(dir, "key.bin")
	writeFile(t, key, []byte("master-key-0123456789"))
	if out, err := run("-k", "-b", "1024", "-enc", "full", "-keyfile", key, in); err != nil {
		t.Fatalf("compress failed: %v\n%s", err, out)
	}
	decDir := t.TempDir()
	ajz := filepath.Join(decDir, "input.ajz")
	copyFile(t, in+".ajz", ajz)

	out, _ := run("-d", "-k", ajz) // no -keyfile
	if !strings.Contains(out, "encrypted") {
		t.Fatalf("expected an 'encrypted; supply -keyfile' message; output:\n%s", out)
	}
}
