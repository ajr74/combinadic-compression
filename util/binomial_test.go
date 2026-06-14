package util

import (
	"math/big"
	"testing"
)

func TestBinCoef_values(t *testing.T) {
	InitCache(5)
	tests := []struct {
		name string
		n    int
		k    int
		want uint64
	}{
		{"zero", 5, 0, 1},
		{"one", 5, 1, 5},
		{"two", 5, 2, 10},
		{"three", 5, 3, 10},
		{"four", 5, 4, 5},
		{"five", 5, 5, 1},
		{"six", 5, 6, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BinCoef(tt.n, tt.k).Value.Uint64(); got != tt.want {
				t.Errorf("BinCoefBitLength() = %v, want %v", got, tt.want)
			}
		})
	}
}

// binomialReference computes C(n,k) exactly and independently of the cache,
// using the multiplicative formula, to serve as ground truth below.
func binomialReference(n, k int) *big.Int {
	if k < 0 || k > n {
		return big.NewInt(0)
	}
	if k > n-k {
		k = n - k
	}
	res := big.NewInt(1)
	tmp := new(big.Int)
	for i := 0; i < k; i++ {
		res.Mul(res, tmp.SetInt64(int64(n-i)))
		res.Div(res, tmp.SetInt64(int64(i+1)))
	}
	return res
}

// TestBinCoef_reference validates the cached coefficients against the
// independent reference across the whole triangle up to N, including values far
// beyond uint64 (e.g. C(200,100) has ~196 bits). This exercises the cache
// construction at scale and both symmetry branches of BinCoef.
func TestBinCoef_reference(t *testing.T) {
	const N = 200
	InitCache(N + 10)
	for n := 0; n <= N; n++ {
		for k := 0; k <= n; k++ {
			if got := BinCoef(n, k).Value; got.Cmp(binomialReference(n, k)) != 0 {
				t.Fatalf("C(%d,%d) = %s, want %s", n, k, got, binomialReference(n, k))
			}
		}
		// out-of-range k must be zero
		if BinCoef(n, n+1).Value.Sign() != 0 {
			t.Fatalf("C(%d,%d) expected 0", n, n+1)
		}
	}
}

// TestBinCoef_bitLength asserts the precomputed BitLength of every cached
// coefficient matches its value's true bit length. The bit packing in the
// compressor/decompressor relies on this field for field widths, so a mismatch
// would silently corrupt the stream.
func TestBinCoef_bitLength(t *testing.T) {
	const N = 200
	InitCache(N + 10)
	for n := 0; n <= N; n++ {
		for k := 0; k <= n; k++ {
			bc := BinCoef(n, k)
			if int(bc.BitLength) != bc.Value.BitLen() {
				t.Fatalf("C(%d,%d): BitLength=%d, Value.BitLen()=%d (value %s)",
					n, k, bc.BitLength, bc.Value.BitLen(), bc.Value)
			}
		}
	}
}

// TestInitCache_resize verifies InitCache can be re-invoked to grow or shrink
// the cache (as the CLI does per run) while BinCoef stays correct afterwards.
func TestInitCache_resize(t *testing.T) {
	InitCache(20)
	if got := BinCoef(10, 5).Value.Uint64(); got != 252 {
		t.Fatalf("after InitCache(20): C(10,5)=%d, want 252", got)
	}
	InitCache(120) // grow
	if got := BinCoef(100, 50).Value; got.Cmp(binomialReference(100, 50)) != 0 {
		t.Fatalf("after grow: C(100,50)=%s, want %s", got, binomialReference(100, 50))
	}
	InitCache(15) // shrink
	if got := BinCoef(10, 5).Value.Uint64(); got != 252 {
		t.Fatalf("after shrink: C(10,5)=%d, want 252", got)
	}
}
