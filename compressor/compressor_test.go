package compressor

import (
	"ajz/util"
	"bytes"
	"encoding/hex"
	"math/big"
	"math/rand"
	"sort"
	"testing"

	"github.com/bits-and-blooms/bitset"
)

// randSortedSet returns k distinct values in [0,n), sorted ascending.
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

// contiguityRef is an obviously-correct reference for findContiguityLengths:
// the lengths of maximal runs of consecutive (differ-by-1) values.
func contiguityRef(nums []uint) []int {
	var out []int
	start := 0
	for i := 1; i <= len(nums); i++ {
		if i == len(nums) || nums[i] != nums[i-1]+1 {
			out = append(out, i-start)
			start = i
		}
	}
	return out
}

func Test_findContiguityLengths(t *testing.T) {
	rng := rand.New(rand.NewSource(101))
	lengths := make([]int, 4096)
	for iter := 0; iter < 5000; iter++ {
		// build a sorted set with a mix of runs and gaps
		n := rng.Intn(200)
		nums := make([]uint, 0, n)
		v := uint(rng.Intn(5))
		for len(nums) < n {
			nums = append(nums, v)
			if rng.Intn(3) == 0 {
				v += uint(rng.Intn(4) + 2) // gap -> new run
			} else {
				v++ // extend run
			}
		}
		findContiguityLengths(nums, lengths)
		want := contiguityRef(nums)
		got := lengths[:len(want)]
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("iter %d nums=%v\n got=%v\nwant=%v", iter, nums, got, want)
			}
		}
	}
}

func benchContiguity(b *testing.B, n int, pattern string) {
	nums := make([]uint, n)
	switch pattern {
	case "run": // one long run
		for i := range nums {
			nums[i] = uint(i)
		}
	case "singletons": // no runs (every gap) -> max boundaries
		for i := range nums {
			nums[i] = uint(i * 2)
		}
	default: // mixed: runs of ~4 with gaps
		v := uint(0)
		for i := range nums {
			nums[i] = v
			if i%4 == 3 {
				v += 3
			} else {
				v++
			}
		}
	}
	lengths := make([]int, n)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		findContiguityLengths(nums, lengths)
	}
}

func BenchmarkFindContiguityLengths(b *testing.B) {
	b.Run("run-4096", func(b *testing.B) { benchContiguity(b, 4096, "run") })
	b.Run("singletons-4096", func(b *testing.B) { benchContiguity(b, 4096, "singletons") })
	b.Run("mixed-4096", func(b *testing.B) { benchContiguity(b, 4096, "mixed") })
}

// Test_indexSetToCompressionIndex_matchesExperimental asserts that
// indexSetToCompressionIndex and indexSetToCompressionIndexHockeyStick produce identical ranks
// for a wide range of random index sets — the property that lets either be
// swapped in freely. The shared scratch buffer also exercises
// indexSetToCompressionIndexHockeyStick's stale-data handling (it relies on an early break
// rather than clearing the buffer).
func Test_indexSetToCompressionIndex_matchesExperimental(t *testing.T) {
	const N = 600
	util.InitCache(N + 10)
	rng := rand.New(rand.NewSource(7))
	scratch := make([]int, N)
	plain := new(big.Int)
	exp := new(big.Int)
	for iter := 0; iter < 3000; iter++ {
		set := randSortedSet(rng, rng.Intn(20), N)
		rank(set, plain)
		rankHockeyStick(set, exp, scratch[:len(set)])
		if plain.Cmp(exp) != 0 {
			t.Fatalf("iter %d set=%v: plain=%s experimental=%s", iter, set, plain, exp)
		}
	}
}

func Test_indexSetToCompressionIndex(t *testing.T) {

	tests := []struct {
		name     string
		indexSet []uint
		want     *big.Int
	}{
		{"small_1", []uint{0, 2, 4}, big.NewInt(5)},
		{"small_2", []uint{1, 3, 4}, big.NewInt(8)},
	}
	util.InitCache(100)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := big.NewInt(0)
			rank(tt.indexSet, result)
			if result.Cmp(tt.want) != 0 {
				t.Errorf("indexSetToCompressionIndex() = %v, want %v", result, tt.want)
			}
		})
	}
}

func BenchmarkProcess(b *testing.B) {
	const block = 4096
	util.InitCache(block + 10)

	// Deterministic, mildly repetitive input so blocks have realistic byte
	// distributions (and contiguous runs to exercise the experimental ranker).
	inputBytes := make([]byte, block)
	seed := uint32(2463534242)
	for i := range inputBytes {
		seed ^= seed << 13
		seed ^= seed >> 17
		seed ^= seed << 5
		inputBytes[i] = byte(seed) % 64 // restrict alphabet -> repeated values
	}

	referenceAlphabetBitSet := bitset.New(256)
	for _, v := range inputBytes {
		referenceAlphabetBitSet.Set(uint(v))
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Process(inputBytes, referenceAlphabetBitSet)
	}
}

// BenchmarkRankColdStart measures one rank accumulation into a fresh big.Int
// (the cold-block case), with and without pre-growing the accumulator. This
// isolates the effect of util.GrowBigInt, which is otherwise hidden once a
// pooled accumulator has reached its high-water capacity.
func BenchmarkRankColdStart(b *testing.B) {
	const n = 4096
	const k = 700
	util.InitCache(n + 10)
	positions := make([]uint, k)
	for i := range positions {
		positions[i] = uint(i * (n / k)) // spread across the block
	}
	scratch := make([]int, n)
	bound := uint(util.BinCoef(n, k).BitLength)

	b.Run("no-pregrow", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			r := new(big.Int)
			rankHockeyStick(positions, r, scratch)
		}
	})
	b.Run("pregrow", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			r := new(big.Int)
			util.GrowBigInt(r, bound)
			rankHockeyStick(positions, r, scratch)
		}
	})
}

// BenchmarkBucketing isolates the position-bucketing step on cold buffers (the
// first block a fresh worker handles), where step #1's pooling cannot yet have
// amortized the per-byte slice growth. It contrasts the old 256-append-slices
// layout against the counting-sort layout.
func BenchmarkBucketing(b *testing.B) {
	const n = 4096
	inputBytes := make([]byte, n)
	seed := uint32(2463534242)
	for i := range inputBytes {
		seed ^= seed << 13
		seed ^= seed >> 17
		seed ^= seed << 5
		inputBytes[i] = byte(seed) // full 256-value alphabet
	}

	b.Run("append-cold", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			bytePositions := make([][]uint, 256)
			for j := uint(0); j < n; j++ {
				bytePositions[inputBytes[j]] = append(bytePositions[inputBytes[j]], j)
			}
			_ = bytePositions
		}
	})

	b.Run("counting-cold", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			counts := make([]int, 256)
			offsets := make([]int, 257)
			cursor := make([]int, 256)
			positions := make([]uint, n)
			for _, c := range inputBytes {
				counts[c]++
			}
			for j := 0; j < 256; j++ {
				offsets[j+1] = offsets[j] + counts[j]
			}
			copy(cursor, offsets[:256])
			for j := uint(0); j < n; j++ {
				c := inputBytes[j]
				positions[cursor[c]] = j
				cursor[c]++
			}
			_ = positions
		}
	})
}

// BenchmarkBlockReduction isolates the per-block reduction cost (the loop of
// ReduceIndexSet calls, one per byte value) for a realistic block, so it can be
// compared against BenchmarkProcess to size the reduction's share of total work.
// alphabet=64 matches BenchmarkProcess (text-like); alphabet=256 is the
// high-entropy worst case.
func BenchmarkBlockReduction(b *testing.B) {
	b.Run("alphabet64", func(b *testing.B) { benchBlockReduction(b, 64) })
	b.Run("alphabet256", func(b *testing.B) { benchBlockReduction(b, 256) })
}

func benchBlockReduction(b *testing.B, alphabet int) {
	const W = 4096
	rng := rand.New(rand.NewSource(5))
	input := make([]byte, W)
	for i := range input {
		input[i] = byte(rng.Intn(alphabet))
	}
	counts := make([]int, 256)
	for _, c := range input {
		counts[c]++
	}
	offsets := make([]int, 257)
	for i := 0; i < 256; i++ {
		offsets[i+1] = offsets[i] + counts[i]
	}
	positions := make([]uint, W)
	cursor := make([]int, 256)
	copy(cursor, offsets[:256])
	for i := 0; i < W; i++ {
		c := input[i]
		positions[cursor[c]] = uint(i)
		cursor[c]++
	}
	var keys []int
	for v := 0; v < 256; v++ {
		if counts[v] > 0 {
			keys = append(keys, v)
		}
	}
	work := make([]uint, W)
	toRemove := bitset.MustNew(W)
	b.ReportAllocs()
	b.ResetTimer()
	for iter := 0; iter < b.N; iter++ {
		toRemove.ClearAll()
		for _, key := range keys {
			seg := positions[offsets[key]:offsets[key+1]]
			n := copy(work, seg) // ReduceIndexSet mutates; work on a copy
			util.ReduceIndexSet(work[:n], toRemove)
		}
	}
}

func Test_indexSetToCompressionIndexHockeyStick(t *testing.T) {

	tests := []struct {
		name     string
		indexSet []uint
		want     *big.Int
	}{
		{"small_1", []uint{0, 2, 4}, big.NewInt(5)},
		{"small_2", []uint{1, 3, 4}, big.NewInt(8)},
	}
	util.InitCache(100)
	scratch := make([]int, 10)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := big.NewInt(0)
			rankHockeyStick(tt.indexSet, result, scratch)
			if result.Cmp(tt.want) != 0 {
				t.Errorf("indexSetToCompressionIndexHockeyStick() = %v, want %v", result, tt.want)
			}
		})
	}
}

// TestWorkedExamplePayload pins the packed-block bytes for the 20-byte worked
// example used in the paper (Figure 1): the block "dbcbceaeecbacdcecbeb" over the
// alphabet {a,b,c,d,e}, compressed in per-record (self-contained) mode -- i.e.
// with an empty reference alphabet, exactly as the -noref CLI flag does.
//
// Figure 1 draws this block bit-for-bit, so this test verifies that drawing and
// guards the serialization against accidental change. The 106 packed bits are,
// in order (each field most-significant-bit first; the final byte is zero-padded
// in its low bits by BitWriter.Bytes):
//
//	cardinality  c = 5           9  bits   000000101
//	alphabet rank  = 83291669    34 bits   colex rank of {97..101} in C(256,5)
//	most-freq byte = 'c' = 99    8  bits   01100011
//	count width    = 3           5  bits   bits.Len(maxK), maxK = 5 over a,b,d,e
//	a: count 2 (3b), rank 61  (8b)
//	b: count 5 (3b), rank 7641 (14b)
//	d: count 2 (3b), rank 28  (7b)
//	e: count 5 (3b), rank 331  (9b)
//	                             --------
//	                             106 bits -> 14 bytes
//
// Total payload: 02809eddc2ac6347b5dd9472d2c0.
func TestWorkedExamplePayload(t *testing.T) {
	// The alphabet rank ranks the 5-symbol alphabet within all C(256,5) subsets,
	// so the binomial cache must cover n = 256 (as well as the block size, 20).
	util.InitCache(256)

	input := []byte("dbcbceaeecbacdcecbeb") // 20 bytes, byte values 'a'..'e' = 97..101
	emptyReference := bitset.New(256)       // empty => per-record mode (the -noref path)

	got := Process(input, emptyReference)

	const wantHex = "02809eddc2ac6347b5dd9472d2c0" // 106 bits, padded to 14 bytes
	want, err := hex.DecodeString(wantHex)
	if err != nil {
		t.Fatalf("malformed wantHex constant: %v", err)
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("packed payload does not match Figure 1:\n got  = %s (%d bytes)\n want = %s (%d bytes)",
			hex.EncodeToString(got), len(got), wantHex, len(want))
	}
	t.Logf("packed payload = %s (%d bytes, 106 bits) — matches Figure 1",
		hex.EncodeToString(got), len(got))
}
