package decompressor

import (
	"ajz/compressor"
	"ajz/util"
	"bytes"
	"math/big"
	"math/rand"
	"sort"
	"testing"

	"github.com/bits-and-blooms/bitset"
)

func equalUintSlices(a, b []uint) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func randSortedSet(rng *rand.Rand, k, n int) []uint {
	if k > n {
		k = n
	}
	chosen := rng.Perm(n)[:k]
	sort.Ints(chosen)
	out := make([]uint, k)
	for i, v := range chosen {
		out[i] = uint(v)
	}
	return out
}

func makeBlock(rng *rand.Rand, n int, mode string) []byte {
	b := make([]byte, n)
	switch mode {
	case "single": // one repeated byte
		fill := byte(rng.Intn(256))
		for i := range b {
			b[i] = fill
		}
	case "low": // small alphabet, many runs
		for i := range b {
			b[i] = byte(rng.Intn(8))
		}
	case "sequential": // full alphabet, contiguous runs
		for i := range b {
			b[i] = byte(i % 256)
		}
	default: // uniform random
		for i := range b {
			b[i] = byte(rng.Intn(256))
		}
	}
	return b
}

// TestCompressDecompressRoundTrip is the key integration test: it compresses a
// block and decompresses it through the real Process functions of both packages
// and asserts exact recovery. This exercises the combinadic rank/unrank pair, the
// reduction logic, the bit codecs, and the alphabet XOR end to end across a
// range of block sizes and byte distributions. A fixed non-trivial alphabet makes
// the alphabet XOR non-degenerate.
func TestCompressDecompressRoundTrip(t *testing.T) {
	util.InitCache(1024 + 10) // covers max block and the 256-value alphabet ranking
	rng := rand.New(rand.NewSource(11))

	referenceAlphabet := bitset.New(256)
	for i := 0; i < 256; i++ {
		if rng.Intn(2) == 0 {
			referenceAlphabet.Set(uint(i))
		}
	}

	sizes := []int{1, 2, 5, 16, 255, 256, 257, 700, 1000}
	modes := []string{"random", "low", "single", "sequential"}
	for _, n := range sizes {
		for _, mode := range modes {
			input := makeBlock(rng, n, mode)
			compressed := compressor.Process(input, referenceAlphabet)
			out := make([]byte, n)
			Process(compressed, uint(n), referenceAlphabet, out)
			if !bytes.Equal(out, input) {
				t.Fatalf("round-trip mismatch n=%d mode=%s\n in=%v\nout=%v", n, mode, input, out)
			}
		}
	}
}

// TestCompressionIndexToBitSet_roundTrip verifies the unranking inverts the
// colexicographic rank. The rank is computed here with the same formula the
// compressor uses (sum of binomial coefficients), so this isolates the decoder.
func TestCompressionIndexToBitSet_roundTrip(t *testing.T) {
	const N = 500
	util.InitCache(N + 10)
	rng := rand.New(rand.NewSource(13))
	result := bitset.New(N)
	for iter := 0; iter < 2000; iter++ {
		set := randSortedSet(rng, rng.Intn(15)+1, N)
		rank := new(big.Int)
		for j := 1; j <= len(set); j++ { // r = sum_{j=1..k} C(set[j-1], j)
			rank.Add(rank, util.BinCoef(int(set[j-1]), j).Value)
		}
		work := new(big.Int).Set(rank) // compressionIndexToBitSet mutates its argument
		unrank(work, uint(len(set)), N, result)
		if got := util.GetIndexSet(result); !equalUintSlices(got, set) {
			t.Fatalf("iter %d: set=%v rank=%s got=%v", iter, set, rank, got)
		}
	}
}

// TestRankEncryptionRoundTrip demonstrates the compression+encryption variant
// end to end on the real codec primitives: a set is ranked (as the compressor
// would), the rank is encrypted in place within [0, C(N,k)), and the
// decryptor recovers it and unranks back to the original set. A wrong key yields
// a different rank that unranks to a different (still valid) set, i.e. the
// ciphertext hides the content.
func TestRankEncryptionRoundTrip(t *testing.T) {
	const N = 400
	util.InitCache(N + 10)
	rng := rand.New(rand.NewSource(31))
	key := []byte("a-secret-key")
	wrong := []byte("a-secret-keZ")
	result := bitset.New(N)
	wrongRecoveries := 0
	const iters = 1000
	for i := 0; i < iters; i++ {
		set := randSortedSet(rng, rng.Intn(15)+1, N)
		k := uint(len(set))

		// rank via the compressor's colexicographic encoding
		rank := new(big.Int)
		for j := 1; j <= len(set); j++ {
			rank.Add(rank, util.BinCoef(int(set[j-1]), j).Value)
		}
		modulus := util.BinCoef(N, int(k)).Value // C(N,k): number of k-subsets

		nonce := uint64(i)
		ct := util.EncryptRank(rank, modulus, key, nonce)
		if ct.Sign() < 0 || ct.Cmp(modulus) >= 0 {
			t.Fatalf("iter %d: ciphertext escaped [0, C(N,k))", i)
		}

		// correct key: decrypt then unrank recovers the original set
		pt := util.DecryptRank(ct, modulus, key, nonce)
		unrank(new(big.Int).Set(pt), k, N, result)
		if got := util.GetIndexSet(result); !equalUintSlices(got, set) {
			t.Fatalf("iter %d: set not recovered with correct key", i)
		}

		// wrong key: should (almost surely) not reproduce the original set
		bad := util.DecryptRank(ct, modulus, wrong, nonce)
		if bad.Cmp(rank) == 0 {
			wrongRecoveries++
		}
	}
	if wrongRecoveries > 0 {
		t.Fatalf("wrong key recovered the rank %d/%d times", wrongRecoveries, iters)
	}
}

func TestCompressionIndexToBitSet_edge(t *testing.T) {
	util.InitCache(300)
	result := bitset.New(256)

	// k = 0 -> empty set
	unrank(big.NewInt(0), 0, 256, result)
	if c := result.Count(); c != 0 {
		t.Errorf("k=0 expected empty set, got count %d", c)
	}

	// k = 1 -> a single bit at the index value
	for _, pos := range []uint{0, 1, 42, 255} {
		unrank(new(big.Int).SetUint64(uint64(pos)), 1, 256, result)
		got := util.GetIndexSet(result)
		if len(got) != 1 || got[0] != pos {
			t.Errorf("k=1 index=%d: got %v", pos, got)
		}
	}
}
