package util

import (
	"math/big"
	"math/rand"
	"testing"
)

// TestRankCipher exercises the size-preserving rank cipher: the ciphertext
// stays in [0, modulus) (no size growth), the correct key round-trips exactly,
// and a wrong key almost never recovers the plaintext.
func TestRankCipher(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	key := []byte("correct-horse-battery-staple")
	wrong := []byte("correct-horse-battery-stapld")
	wrongRecoveries := 0
	const iters = 4000
	for i := 0; i < iters; i++ {
		// random modulus M >= 2 and rank in [0, M)
		bitLen := rng.Intn(2000) + 1
		modulus := new(big.Int).Lsh(big.NewInt(1), uint(bitLen))
		modulus.Add(modulus, big.NewInt(int64(rng.Intn(1000)+2))) // M >= 2
		rank := new(big.Int).Rand(rng, modulus)
		nonce := uint64(i)

		ct := EncryptRank(rank, modulus, key, nonce)
		if ct.Sign() < 0 || ct.Cmp(modulus) >= 0 {
			t.Fatalf("iter %d: ciphertext out of range [0,M)", i)
		}
		if got := DecryptRank(ct, modulus, key, nonce); got.Cmp(rank) != 0 {
			t.Fatalf("iter %d: round-trip mismatch", i)
		}
		if DecryptRank(ct, modulus, wrong, nonce).Cmp(rank) == 0 {
			wrongRecoveries++
		}
	}
	if wrongRecoveries > 0 {
		t.Fatalf("wrong key recovered the plaintext %d/%d times", wrongRecoveries, iters)
	}
}

// TestRankCipher_formatPreserving confirms the ciphertext never needs more bits
// than the plaintext rank's field width, ceil(log2 M).
func TestRankCipher_formatPreserving(t *testing.T) {
	key := []byte("k")
	for _, bits := range []uint{8, 64, 256, 1000} {
		modulus := new(big.Int).Lsh(big.NewInt(1), bits) // M = 2^bits
		field := modulus.BitLen() - 1                    // ceil(log2 M) for M = 2^bits is `bits`
		rank := new(big.Int).Sub(modulus, big.NewInt(1)) // largest rank
		ct := EncryptRank(rank, modulus, key, 1)
		if ct.BitLen() > field+1 {
			t.Fatalf("M=2^%d: ciphertext uses %d bits, field is %d", bits, ct.BitLen(), field)
		}
	}
}

// edgeModuli spans the small field ranges the broad random test above skips but
// the codec relies on: M=1 (the eliminated final value, sole admissible value
// 0), the tiny moduli of a count k_i over [0,n_i] or the most-frequent index
// over [0,c), and the alphabet-cardinality boundary around 256/257, alongside a
// power of two and large non-power-of-two ranks.
func edgeModuli() []*big.Int {
	ms := []*big.Int{
		big.NewInt(1), big.NewInt(2), big.NewInt(3), big.NewInt(5),
		big.NewInt(255), big.NewInt(256), big.NewInt(257), big.NewInt(1 << 16),
	}
	for _, bits := range []uint{64, 128, 200, 256} {
		m := new(big.Int).Lsh(big.NewInt(1), bits)
		m.Sub(m, big.NewInt(1)) // 2^bits - 1: large and not a power of two
		ms = append(ms, m)
	}
	return ms
}

// TestRankCipherEdgeModuli round-trips every value (for the tiny moduli) or many
// random values (for the large ones), confirming correctness and in-range
// ciphertext across the field sizes the random test does not reach.
func TestRankCipherEdgeModuli(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	key := []byte("edge-key")
	for _, m := range edgeModuli() {
		iters := 200
		small := m.IsInt64() && m.Int64() <= 256
		if small {
			iters = int(m.Int64()) // exhaustive for tiny moduli
		}
		for i := 0; i < iters; i++ {
			var r *big.Int
			if small {
				r = big.NewInt(int64(i))
			} else {
				r = new(big.Int).Rand(rng, m)
			}
			nonce := rng.Uint64()
			c := EncryptRank(r, m, key, nonce)
			if c.Sign() < 0 || c.Cmp(m) >= 0 {
				t.Fatalf("M=%v: ciphertext %v out of range", m, c)
			}
			if back := DecryptRank(c, m, key, nonce); back.Cmp(r) != 0 {
				t.Fatalf("M=%v nonce=%d: round-trip r=%v -> c=%v -> %v", m, nonce, r, c, back)
			}
		}
	}
}

// TestEncryptDecryptInverse checks the other direction: Encrypt(Decrypt(c)) == c
// for an arbitrary ciphertext in [0,M).
func TestEncryptDecryptInverse(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	key := []byte("inverse-key")
	for _, m := range edgeModuli() {
		for i := 0; i < 100; i++ {
			c := new(big.Int).Rand(rng, m)
			nonce := rng.Uint64()
			r := DecryptRank(c, m, key, nonce)
			if back := EncryptRank(r, m, key, nonce); back.Cmp(c) != 0 {
				t.Fatalf("M=%v nonce=%d: inverse c=%v -> r=%v -> %v", m, nonce, c, r, back)
			}
		}
	}
}

// TestKeystreamDeterministic: the same (key, nonce, modulus) must yield the same
// keystream, or decryption could not invert encryption.
func TestKeystreamDeterministic(t *testing.T) {
	key := []byte("k")
	m := big.NewInt(1 << 20)
	if rankKeystream(key, 42, m).Cmp(rankKeystream(key, 42, m)) != 0 {
		t.Fatalf("keystream not deterministic for fixed (key,nonce,M)")
	}
}

// TestNonceReuseLeaksDifference documents *why* the caller must supply a unique
// nonce per field: reusing one degenerates to a two-time pad, under which the
// ciphertext difference equals the plaintext difference (mod M). Asserted here
// so the integration's nonce discipline cannot regress silently.
func TestNonceReuseLeaksDifference(t *testing.T) {
	key := []byte("k")
	m := big.NewInt(1000003) // prime, for a clean modular difference
	nonce := uint64(7)
	r1, r2 := big.NewInt(111), big.NewInt(222)
	c1 := EncryptRank(r1, m, key, nonce)
	c2 := EncryptRank(r2, m, key, nonce)

	lhs := new(big.Int).Sub(c1, c2)
	lhs.Mod(lhs, m)
	rhs := new(big.Int).Sub(r1, r2)
	rhs.Mod(rhs, m)
	if lhs.Cmp(rhs) != 0 {
		t.Fatalf("nonce reuse should preserve the plaintext difference: %v != %v", lhs, rhs)
	}
}

// TestDifferentNonceDecorrelates: distinct nonces yield distinct keystreams with
// overwhelming probability, so fields under different nonces avoid the
// two-time-pad relation above.
func TestDifferentNonceDecorrelates(t *testing.T) {
	key := []byte("k")
	m := new(big.Int).Lsh(big.NewInt(1), 128)
	if rankKeystream(key, 1, m).Cmp(rankKeystream(key, 2, m)) == 0 {
		t.Fatalf("distinct nonces produced identical keystreams (astronomically unlikely)")
	}
}

// TestInputsNotMutated: the helpers must not mutate their rank or modulus
// arguments, which are shared big.Int values in the pipeline.
func TestInputsNotMutated(t *testing.T) {
	m := big.NewInt(123457)
	r := big.NewInt(42)
	mCopy, rCopy := new(big.Int).Set(m), new(big.Int).Set(r)
	_ = EncryptRank(r, m, []byte("k"), 1)
	if m.Cmp(mCopy) != 0 {
		t.Fatalf("EncryptRank mutated the modulus: %v != %v", m, mCopy)
	}
	if r.Cmp(rCopy) != 0 {
		t.Fatalf("EncryptRank mutated the rank: %v != %v", r, rCopy)
	}
}

// TestModulusOneIsIdentity: a fully determined field has M=1, whose only value
// is 0; the cipher must map 0 to 0 and back.
func TestModulusOneIsIdentity(t *testing.T) {
	m := big.NewInt(1)
	if c := EncryptRank(big.NewInt(0), m, []byte("k"), 99); c.Sign() != 0 {
		t.Fatalf("M=1 ciphertext should be 0, got %v", c)
	}
	if back := DecryptRank(big.NewInt(0), m, []byte("k"), 99); back.Sign() != 0 {
		t.Fatalf("M=1 round-trip should be 0, got %v", back)
	}
}

// TestEncryptUintRoundTrip covers the small-field cipher used for the
// cardinality, most-frequent value, count-width header, and counts: round-trip
// over power-of-two and odd moduli, in-range ciphertext, and the M<=1 identity.
func TestEncryptUintRoundTrip(t *testing.T) {
	key := []byte("uint-key")
	moduli := []uint64{1, 2, 256, 257, 512, 1 << 16}
	for _, m := range moduli {
		for v := uint64(0); v < m && v < 600; v++ {
			for _, nonce := range []uint64{0, 1, 12345} {
				c := EncryptUint(v, m, key, nonce)
				if m > 1 && c >= m {
					t.Fatalf("M=%d: ciphertext %d out of range", m, c)
				}
				if back := DecryptUint(c, m, key, nonce); back != v && m > 1 {
					t.Fatalf("M=%d nonce=%d: round-trip %d -> %d -> %d", m, nonce, v, c, back)
				}
			}
		}
	}
}

// TestKeystreamCoverageSmallModulus is a light bias sanity check: over many
// nonces the keystream residues mod a small M should be roughly uniform. With
// M=3 and 30000 samples the expected count per residue is 10000 (std dev ~81),
// so a 20% band is far outside any plausible fluctuation and will not flake,
// while still catching a gross modular bias.
func TestKeystreamCoverageSmallModulus(t *testing.T) {
	key := []byte("k")
	m := big.NewInt(3)
	const N = 30000
	counts := make([]int, 3)
	for n := 0; n < N; n++ {
		counts[rankKeystream(key, uint64(n), m).Int64()]++
	}
	exp := N / 3
	for v, c := range counts {
		if c == 0 {
			t.Fatalf("residue %d never produced over %d samples", v, N)
		}
		if c < exp*8/10 || c > exp*12/10 {
			t.Errorf("residue %d count %d far from expected ~%d (possible bias)", v, c, exp)
		}
	}
}
