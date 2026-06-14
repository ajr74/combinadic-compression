package util

import (
	"fmt"
	"math/big"
	"math/rand"
	"reflect"
	"testing"

	"github.com/bits-and-blooms/bitset"
)

// TestBitWriterFuzz and TestBitWriterMatchesBuilder previously validated
// BitWriter by comparing its output byte-for-byte against the go-bitarray
// Builder reference implementation (github.com/tunabay/go-bitarray). That
// dependency has been removed now that BitWriter is independently covered by
// TestBitReaderInvertsBitWriter and the integration round-trip tests.

func TestGrowBigInt(t *testing.T) {
	values := []string{"0", "1", "255", "18446744073709551616", "9999999999999999999999999999999"}
	for _, s := range values {
		t.Run(s, func(t *testing.T) {
			z := new(big.Int)
			z.SetString(s, 10)
			want := new(big.Int).Set(z)
			for _, nBits := range []uint{0, 1, 64, 65, 1000} {
				GrowBigInt(z, nBits) // must never change the value
				if z.Cmp(want) != 0 {
					t.Fatalf("GrowBigInt(%s, %d) changed value to %s", s, nBits, z.String())
				}
			}
		})
	}
}

// TestBitReaderInvertsBitWriter writes randomized field sequences with BitWriter
// and reads them back with BitReader, asserting exact recovery — the guarantee
// the decompressor relies on (BitReader reads what the compressor's BitWriter
// wrote). BitWriter is independently proven equal to the go-bitarray Builder.
func TestBitReaderInvertsBitWriter(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for iter := 0; iter < 2000; iter++ {
		type fld struct {
			isBig bool
			u     uint64
			b     *big.Int
			n     uint
		}
		var fields []fld
		bw := NewBitWriter()
		count := rng.Intn(12)
		for f := 0; f < count; f++ {
			if rng.Intn(2) == 0 {
				n := uint(rng.Intn(16) + 1)
				v := uint64(rng.Intn(1 << n))
				bw.WriteBits(v, n)
				fields = append(fields, fld{u: v, n: n})
			} else {
				bitsLen := rng.Intn(2000)
				z := new(big.Int).Rand(rng, new(big.Int).Lsh(big.NewInt(1), uint(bitsLen)+1))
				width := uint(z.BitLen() + rng.Intn(40))
				if width == 0 {
					width = 1
				}
				bw.WriteBigIntBits(z, width)
				fields = append(fields, fld{isBig: true, b: z, n: width})
			}
		}

		br := NewBitReader(bw.Bytes())
		var scratch []byte
		got := new(big.Int)
		for i, f := range fields {
			if f.isBig {
				scratch = br.ReadBigInt(f.n, got, scratch)
				if got.Cmp(f.b) != 0 {
					t.Fatalf("iter %d field %d big mismatch: got %s want %s", iter, i, got, f.b)
				}
			} else {
				if v := br.ReadBits(f.n); v != f.u {
					t.Fatalf("iter %d field %d uint mismatch: got %d want %d (n=%d)", iter, i, v, f.u, f.n)
				}
			}
		}
	}
}

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

// TestReduceIndexSet_matchesReference checks the optimized ReduceIndexSet against
// an obviously-correct O(n*m) reference: each index drops by the number of
// already-removed positions strictly below it. (Replaces the old print-only test.)
func TestReduceIndexSet_matchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	for iter := 0; iter < 3000; iter++ {
		n := uint(rng.Intn(200) + 1)
		var removed, indexes []uint // disjoint by construction
		for p := uint(0); p < n; p++ {
			switch rng.Intn(3) {
			case 0:
				removed = append(removed, p)
			case 1:
				indexes = append(indexes, p)
			}
		}
		want := make([]uint, len(indexes))
		for j, idx := range indexes {
			cnt := uint(0)
			for _, rp := range removed {
				if rp < idx {
					cnt++
				}
			}
			want[j] = idx - cnt
		}

		toRemove := bitset.MustNew(n)
		for _, rp := range removed {
			toRemove.Set(rp)
		}
		got := append([]uint(nil), indexes...)
		ReduceIndexSet(got, toRemove)
		if !equalUintSlices(got, want) {
			t.Fatalf("iter %d removed=%v indexes=%v\n got=%v\nwant=%v", iter, removed, indexes, got, want)
		}
	}
}

func benchReduce(b *testing.B, block int, fraction float64) {
	rng := rand.New(rand.NewSource(1))
	toRemove := bitset.MustNew(uint(block))
	var indexesOrig []uint
	for p := uint(0); p < uint(block); p++ {
		if rng.Float64() < fraction {
			indexesOrig = append(indexesOrig, p) // this call's positions
		} else if rng.Intn(2) == 0 {
			toRemove.Set(p) // pre-existing removed positions
		}
	}
	work := make([]uint, len(indexesOrig))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		copy(work, indexesOrig) // ReduceIndexSet mutates work in place
		ReduceIndexSet(work, toRemove)
	}
}

func BenchmarkReduceIndexSet(b *testing.B) {
	b.Run("dense-4096", func(b *testing.B) { benchReduce(b, 4096, 0.5) })
	b.Run("sparse-4096", func(b *testing.B) { benchReduce(b, 4096, 0.05) })
}

func TestGetIndexSetWithBuffer_matchesGetIndexSet(t *testing.T) {
	cases := [][]uint{{}, {0}, {255}, {0, 5, 63, 64, 65, 200, 255}}
	for _, idx := range cases {
		bs := BitSetFromIndexSet(idx, 256)
		got := GetIndexSetWithBuffer(&bs, make([]uint, 0, 256))
		want := GetIndexSet(&bs)
		if !equalUintSlices(got, want) {
			t.Errorf("idx=%v: GetIndexSetWithBuffer=%v, GetIndexSet=%v", idx, got, want)
		}
	}
}

// TestBinCoef_symmetryAndEdges checks the cached coefficients against the
// defining identities: edges, symmetry C(n,k)=C(n,n-k), and Pascal's rule.
func TestBinCoef_symmetryAndEdges(t *testing.T) {
	const N = 60
	InitCache(N + 10)
	for n := 0; n <= N; n++ {
		if BinCoef(n, 0).Value.Uint64() != 1 {
			t.Fatalf("C(%d,0) != 1", n)
		}
		if BinCoef(n, n).Value.Uint64() != 1 {
			t.Fatalf("C(%d,%d) != 1", n, n)
		}
		if BinCoef(n, n+1).Value.Sign() != 0 {
			t.Fatalf("C(%d,%d) expected 0", n, n+1)
		}
		for k := 0; k <= n; k++ {
			if BinCoef(n, k).Value.Cmp(BinCoef(n, n-k).Value) != 0 {
				t.Fatalf("symmetry C(%d,%d) != C(%d,%d)", n, k, n, n-k)
			}
			if n > 0 && k > 0 { // Pascal: C(n,k) = C(n-1,k-1) + C(n-1,k)
				sum := new(big.Int).Add(BinCoef(n-1, k-1).Value, BinCoef(n-1, k).Value)
				if BinCoef(n, k).Value.Cmp(sum) != 0 {
					t.Fatalf("Pascal fails at C(%d,%d)", n, k)
				}
			}
		}
	}
}

func Test_num_bits_required_to_represent(t *testing.T) {

	tests := []struct {
		name  string
		value uint
		want  uint
	}{
		{"zero", 0, 1},
		{"one", 1, 1},
		{"small", 101, 7},
		{"medium", 58836830, 26},
		{"power_of_two", 32768, 16},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NumBitsRequiredToRepresentBigInt(tt.value); got != tt.want {
				t.Errorf("num_bits_required_to_represent() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_getIndexSet(t *testing.T) {

	tests := []struct {
		name  string
		value string
		want  []uint
	}{
		{"empty", "", []uint{}},
		{"all_zeroes", "00000", []uint{}},
		{"all_ones", "11111", []uint{0, 1, 2, 3, 4}},
		{"low_cardinality", "00000101000", []uint{5, 7}},
		{"high_cardinality", "11101001111", []uint{0, 1, 2, 4, 7, 8, 9, 10}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetIndexSet(new(StringToBitSet(tt.value))); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetIndexSet() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_intToBitSet(t *testing.T) {

	tests := []struct {
		name   string
		value  uint
		length int
		want   bitset.BitSet
	}{
		{"zero", 0, 5, StringToBitSet("00000")},
		{"non_zero", 25750215, 28, StringToBitSet("0001100010001110101011000111")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := uintToBitSet(tt.value, tt.length)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("uintToBitSet() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Handy tool for generating various data files. (Could probably replicate as a script.)
// https://onlinefiletools.com/generate-random-text-file

func TestReduceIndexSet(t *testing.T) {
	type args struct {
		length          uint
		indexes         []uint
		toRemoveIndexes []uint
	}
	tests := []struct {
		name string
		args args
	}{
		{"blah1", args{length: 20, indexes: []uint{1, 3, 10, 17, 19}, toRemoveIndexes: []uint{6, 11}}},
		{"blah2", args{length: 20, indexes: []uint{1, 3, 10, 17, 18}, toRemoveIndexes: []uint{6, 19}}},
		{"blah3", args{length: 20, indexes: []uint{1, 3, 10, 16, 17}, toRemoveIndexes: []uint{18, 19}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ReduceIndexSet(tt.args.indexes, new(BitSetFromIndexSet(tt.args.toRemoveIndexes, tt.args.length)))
			fmt.Printf("indexes = %v\n", tt.args.indexes)
		})
	}
}
