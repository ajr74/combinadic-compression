package decompressor_test

import (
	"fmt"
	"math/rand"
	"testing"

	"ajz/compressor"
	"ajz/decompressor"
	"ajz/util"

	"github.com/bits-and-blooms/bitset"
)

// benchInput builds a deterministic, mildly text-like block: bytes over a
// moderate alphabet with a skew toward the low symbols, so several count and
// rank fields are exercised (the cipher's per-field cost is what we measure).
func benchInput(n, alphabet int, seed int64) []byte {
	rng := rand.New(rand.NewSource(seed))
	b := make([]byte, n)
	for i := range b {
		v := rng.Intn(alphabet)
		if rng.Intn(2) == 0 {
			v = rng.Intn(alphabet/4 + 1) // skew toward low symbols
		}
		b[i] = byte(v)
	}
	return b
}

// BenchmarkEncryption measures the throughput cost of the size-preserving
// cipher: plaintext vs queryable (ranks only) vs full (every field), for both
// compression and decompression, at two block sizes. Run with:
//
//	go test ./decompressor -bench=BenchmarkEncryption -benchmem -run=^$
//
// SetBytes makes Go report MB/s per variant; the encryption overhead is the
// ratio of the plain throughput to the encrypted throughput.
func BenchmarkEncryption(b *testing.B) {
	util.InitCache(4400) // covers C(256,.) and C(<=4096, k)
	base := bitset.MustNew(256)
	key := []byte("benchmark-subkey-0123456789abcd0")
	queryable := util.Cipher{Subkey: key, BlockIndex: 1}
	full := util.Cipher{Subkey: key, BlockIndex: 1, Full: true}

	for _, n := range []int{1024, 4096} {
		input := benchInput(n, 40, int64(n))

		b.Run(fmt.Sprintf("compress/plain/N=%d", n), func(b *testing.B) {
			b.SetBytes(int64(n))
			for i := 0; i < b.N; i++ {
				_ = compressor.Process(input, base)
			}
		})
		b.Run(fmt.Sprintf("compress/queryable/N=%d", n), func(b *testing.B) {
			b.SetBytes(int64(n))
			for i := 0; i < b.N; i++ {
				_ = compressor.ProcessEncrypted(input, base, queryable)
			}
		})
		b.Run(fmt.Sprintf("compress/full/N=%d", n), func(b *testing.B) {
			b.SetBytes(int64(n))
			for i := 0; i < b.N; i++ {
				_ = compressor.ProcessEncrypted(input, base, full)
			}
		})

		// Pre-encode for the decompression benchmarks; assert size identity once.
		plainEnc := compressor.Process(input, base)
		qEnc := compressor.ProcessEncrypted(input, base, queryable)
		fullEnc := compressor.ProcessEncrypted(input, base, full)
		if len(qEnc) != len(plainEnc) || len(fullEnc) != len(plainEnc) {
			b.Fatalf("N=%d: encrypted sizes differ from plaintext (%d, %d vs %d)",
				n, len(qEnc), len(fullEnc), len(plainEnc))
		}
		out := make([]byte, n)

		b.Run(fmt.Sprintf("decompress/plain/N=%d", n), func(b *testing.B) {
			b.SetBytes(int64(n))
			for i := 0; i < b.N; i++ {
				decompressor.Process(plainEnc, uint(n), base, out)
			}
		})
		b.Run(fmt.Sprintf("decompress/queryable/N=%d", n), func(b *testing.B) {
			b.SetBytes(int64(n))
			for i := 0; i < b.N; i++ {
				decompressor.ProcessEncrypted(qEnc, uint(n), base, out, queryable)
			}
		})
		b.Run(fmt.Sprintf("decompress/full/N=%d", n), func(b *testing.B) {
			b.SetBytes(int64(n))
			for i := 0; i < b.N; i++ {
				decompressor.ProcessEncrypted(fullEnc, uint(n), base, out, full)
			}
		})
	}
}
