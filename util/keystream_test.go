package util

import (
	"fmt"
	"math/big"
	"math/rand"
	"testing"
)

// BenchmarkKeystream is the direct cipher-speed comparison: bytes/second of raw
// keystream from each generator at a few field-representative sizes. Run with:
//
//	go test ./util -bench=BenchmarkKeystream -benchmem -run=^$
//
// SetBytes makes Go report MB/s, so the three generators are directly comparable.
func BenchmarkKeystream(b *testing.B) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i * 7)
	}
	gens := []struct {
		name string
		gen  keystreamGen
	}{
		{"sha256", sha256Keystream},
		{"aes", aesCTRKeystream},
		{"chacha20", chacha20Keystream},
	}
	// 64 B ~ a small count/index field; 1 KiB ~ a position rank; 64 KiB ~ a large
	// block's whole keystream.
	for _, n := range []int{64, 1024, 65536} {
		for _, g := range gens {
			b.Run(fmt.Sprintf("%s/%dB", g.name, n), func(b *testing.B) {
				b.SetBytes(int64(n))
				for i := 0; i < b.N; i++ {
					_ = g.gen(key, uint64(i), n)
				}
			})
		}
	}
}

// TestKeystreamSourcesRoundTrip confirms that every keystream source produces a
// correct, in-range, invertible rank cipher --- so AES and ChaCha20 are drop-in
// replacements for the reference generator.
func TestKeystreamSourcesRoundTrip(t *testing.T) {
	defer SetKeystream("sha256") // restore the default for other tests
	rng := rand.New(rand.NewSource(4))
	key := []byte("a-master-key-of-arbitrary-length")
	for _, name := range []string{"sha256", "aes", "chacha20"} {
		SetKeystream(name)
		for _, m := range edgeModuli() { // defined in cipher_test.go (same package)
			for i := 0; i < 50; i++ {
				r := new(big.Int).Rand(rng, m)
				nonce := rng.Uint64()
				c := EncryptRank(r, m, key, nonce)
				if c.Sign() < 0 || c.Cmp(m) >= 0 {
					t.Fatalf("%s: ciphertext %v out of range [0,%v)", name, c, m)
				}
				if back := DecryptRank(c, m, key, nonce); back.Cmp(r) != 0 {
					t.Fatalf("%s: M=%v round-trip %v -> %v -> %v", name, m, r, c, back)
				}
			}
		}
	}
}
