package decompressor_test

import (
	"bytes"
	"math/rand"
	"testing"

	"ajz/compressor"
	"ajz/decompressor"
	"ajz/util"

	"github.com/bits-and-blooms/bitset"
)

// TestEncryptedRankPipelineRoundTrip exercises the rank-only (queryable dial)
// encryption end to end through the real compress/decompress paths: the
// encrypted block is the same size as the plaintext block (format preservation),
// round-trips exactly under the correct cipher, and fails under a wrong key or a
// wrong block index (nonce).
func TestEncryptedRankPipelineRoundTrip(t *testing.T) {
	util.InitCache(700) // covers C(256,.) and C(<=600, k)

	rng := rand.New(rand.NewSource(12345))
	input := make([]byte, 600)
	for i := range input {
		input[i] = byte(rng.Intn(12)) // ~12-symbol alphabet -> several rank fields
	}
	base := bitset.MustNew(256) // per-record: empty base alphabet
	key := []byte("file-subkey-0123456789abcdef0123")
	cipher := util.Cipher{Subkey: key, BlockIndex: 7}

	plain := compressor.Process(input, base)
	enc := compressor.ProcessEncrypted(input, base, cipher)

	// Format preservation: encryption must not change the compressed size.
	if len(enc) != len(plain) {
		t.Fatalf("encrypted size %d != plaintext size %d", len(enc), len(plain))
	}
	// Encryption should actually change the bytes (overwhelmingly likely).
	if bytes.Equal(enc, plain) {
		t.Fatalf("encrypted block identical to plaintext block")
	}

	// Round-trip with the correct cipher.
	out := make([]byte, len(input))
	decompressor.ProcessEncrypted(enc, uint(len(input)), base, out, cipher)
	if !bytes.Equal(out, input) {
		t.Fatalf("encrypted round-trip did not recover the input")
	}

	// Wrong key must not recover the input.
	wrongKey := util.Cipher{Subkey: []byte("wrong-subkey-0123456789abcdef012"), BlockIndex: 7}
	bad := make([]byte, len(input))
	decompressor.ProcessEncrypted(enc, uint(len(input)), base, bad, wrongKey)
	if bytes.Equal(bad, input) {
		t.Fatalf("wrong key recovered the input")
	}

	// Wrong block index (a different nonce) must not recover the input.
	wrongIdx := util.Cipher{Subkey: key, BlockIndex: 8}
	bad2 := make([]byte, len(input))
	decompressor.ProcessEncrypted(enc, uint(len(input)), base, bad2, wrongIdx)
	if bytes.Equal(bad2, input) {
		t.Fatalf("wrong block index recovered the input")
	}
}

// TestFullEncryptionPipelineRoundTrip exercises full-block encryption (every
// field: cardinality, alphabet rank, most-frequent value, count-width header,
// every count and rank). The encrypted block is the same size as the plaintext
// block, round-trips exactly under the correct cipher, and does not recover the
// input under a wrong key. Without per-block authentication a wrong key may also
// cause the decoder to bail or panic, which we tolerate as an acceptable failure
// mode and guard with recover().
func TestFullEncryptionPipelineRoundTrip(t *testing.T) {
	util.InitCache(700)

	rng := rand.New(rand.NewSource(54321))
	input := make([]byte, 600)
	for i := range input {
		input[i] = byte(rng.Intn(20))
	}
	base := bitset.MustNew(256)
	key := []byte("file-subkey-0123456789abcdef0123")
	cipher := util.Cipher{Subkey: key, BlockIndex: 3, Full: true}

	plain := compressor.Process(input, base)
	enc := compressor.ProcessEncrypted(input, base, cipher)

	if len(enc) != len(plain) {
		t.Fatalf("full-mode size %d != plaintext size %d", len(enc), len(plain))
	}

	out := make([]byte, len(input))
	decompressor.ProcessEncrypted(enc, uint(len(input)), base, out, cipher)
	if !bytes.Equal(out, input) {
		t.Fatalf("full-mode round-trip did not recover the input")
	}

	wrong := util.Cipher{Subkey: []byte("wrong-subkey-0123456789abcdef012"), BlockIndex: 3, Full: true}
	bad := make([]byte, len(input))
	func() {
		defer func() { _ = recover() }() // unauthenticated: a wrong key may panic
		decompressor.ProcessEncrypted(enc, uint(len(input)), base, bad, wrong)
	}()
	if bytes.Equal(bad, input) {
		t.Fatalf("wrong key recovered the input in full mode")
	}
}

// TestPlaintextPathUnchanged confirms the non-encrypting Process still produces a
// block that the non-encrypting decompressor recovers exactly (the cipher=nil
// path must be byte-identical to the original behaviour).
func TestPlaintextPathUnchanged(t *testing.T) {
	util.InitCache(700)
	rng := rand.New(rand.NewSource(999))
	input := make([]byte, 512)
	for i := range input {
		input[i] = byte(rng.Intn(30))
	}
	base := bitset.MustNew(256)

	packed := compressor.Process(input, base)
	out := make([]byte, len(input))
	decompressor.Process(packed, uint(len(input)), base, out)
	if !bytes.Equal(out, input) {
		t.Fatalf("plaintext round-trip failed")
	}
}
