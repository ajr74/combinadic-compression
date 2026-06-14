package util

import (
	"math/big"
)

// Size-preserving rank cipher.
//
// Each block of the compressor encodes its content as a colexicographic rank
// r in [0, M), where M is a product of binomial coefficients known to both
// encoder and decoder. Because the compressed form is an integer over a known
// finite range -- rather than a self-delimiting bitstream -- it can be encrypted
// in place by an additive cipher over Z_M:
//
//	c = (r + s) mod M        (encrypt)
//	r = (c - s) mod M        (decrypt)
//
// where s is a key-derived keystream value in [0, M). The ciphertext c is again
// in [0, M), so it occupies exactly the same number of bits as any rank for the
// same M: compression and encryption are combined at no size cost, and the
// ciphertext exposes none of the block's positional structure.
//
// SECURITY NOTE. This is a prototype illustrating the construction, not a
// hardened cipher. The keystream is a unique value per (key, nonce); the caller
// MUST supply a distinct nonce for every rank encrypted under a given key (e.g.
// block number combined with field index) -- reuse degenerates to a two-time
// pad. The keystream here is SHA-256 in counter mode, with a few extra bytes
// before reduction so the modular bias is cryptographically negligible
// (~2^-128); a production system should use a vetted stream cipher (AES-CTR,
// ChaCha20) and, for an exactly-uniform keystream, rejection sampling.
//
// SCOPE. These helpers encrypt one integer field over its modulus. They are not
// limited to the position rank: every field of a block (the alphabet rank over
// C(256,c), the most-frequent value as an index over [0,c), each count k_i over
// [0,n_i], and each position rank r_i over [0,C(n_i,k_i))) is an integer over a
// range known to both parties, so each admits the same additive cipher. The
// decisive property is that each field's modulus depends only on the block
// length and the fields already decoded, never on the field's own value, so the
// fields decrypt in the decoder's existing forward pass and the whole block can
// be encrypted -- the "widths derive from the counts" worry applies only to
// random access WITHIN a block, which the format never needs.
//
// Which fields one encrypts is a leakage/queryability dial with the counts as the
// pivot, since the variable-width ranks make cleartext counts a skip-table over
// the encrypted payload:
//   - Encrypt the ranks only, leave the counts clear: a reader can seek past the
//     encrypted ranks and selectively decrypt one symbol; an observer learns the
//     count spectrum (histogram shape / order-0 entropy) but not the byte labels.
//   - Encrypt every field: no skip-table, strictly front-to-back decryption, and
//     the only residual leak is the per-block compressed length (unavoidable for
//     any size-preserving joint scheme).
// For full encryption the caller must also encrypt the stream-global alphabet
// (decrypted once and cached) and encrypt the uniform count-width
// header field, so that field boundaries still follow from the decrypted prefix
// (the decoder recovers the shared count width before it parses the counts).
// Random access ACROSS blocks is preserved either way. The security of the
// additive cipher rests entirely on the keystream, not on the compression.

// rankKeystream derives a keystream value in [0, modulus) deterministically from
// key and nonce. It draws a few more bytes than the modulus needs so that the
// reduction modulo M leaves a cryptographically negligible bias, then reduces. The
// underlying byte generator is selectable (keystream.go): SHA-256 counter mode by
// default and in all reported measurements, with AES-256-CTR and ChaCha20 as
// validated drop-in alternatives (not selected by the CLI).
func rankKeystream(key []byte, nonce uint64, modulus *big.Int) *big.Int {
	if modulus.Sign() <= 0 {
		return big.NewInt(0)
	}
	nBytes := (modulus.BitLen()+7)/8 + 16 // extra bytes shrink the modular bias
	ks := new(big.Int).SetBytes(keystreamSource(key, nonce, nBytes))
	return ks.Mod(ks, modulus)
}

// EncryptRank returns (rank + keystream) mod modulus, a value in [0, modulus).
// rank must satisfy 0 <= rank < modulus. nonce must be unique per rank under a
// given key.
func EncryptRank(rank, modulus *big.Int, key []byte, nonce uint64) *big.Int {
	out := new(big.Int).Add(rank, rankKeystream(key, nonce, modulus))
	return out.Mod(out, modulus)
}

// DecryptRank inverts EncryptRank: (cipher - keystream) mod modulus.
func DecryptRank(cipher, modulus *big.Int, key []byte, nonce uint64) *big.Int {
	out := new(big.Int).Sub(cipher, rankKeystream(key, nonce, modulus))
	return out.Mod(out, modulus) // Euclidean Mod: result in [0, modulus)
}

// EncryptUint and DecryptUint apply the same additive cipher to a small field
// that fits in a uint64: the alphabet cardinality, the most-frequent value, the
// count-width header, and each count, over a modulus equal to the field's value
// space (a power of two for the fixed-width header fields). val must lie in
// [0, modulus). A modulus of 0 or 1 (a fully determined field) maps to 0.
func EncryptUint(val, modulus uint64, key []byte, nonce uint64) uint64 {
	if modulus <= 1 {
		return 0
	}
	ks := rankKeystream(key, nonce, new(big.Int).SetUint64(modulus)).Uint64()
	return (val + ks) % modulus
}

// DecryptUint inverts EncryptUint: (cipher - keystream) mod modulus.
func DecryptUint(cipher, modulus uint64, key []byte, nonce uint64) uint64 {
	if modulus <= 1 {
		return 0
	}
	ks := rankKeystream(key, nonce, new(big.Int).SetUint64(modulus)).Uint64()
	return (cipher + modulus - ks) % modulus // ks in [0,modulus): no underflow
}

// EncryptBytesField encrypts a fixed-length big-endian byte field in place over
// modulus 2^(8*len(b)): used for the 256-bit alphabet set and the 64-bit
// integrity hash, neither of which has a natural rank modulus of its own.
func EncryptBytesField(b, key []byte, nonce uint64) {
	mod := new(big.Int).Lsh(big.NewInt(1), uint(8*len(b)))
	EncryptRank(new(big.Int).SetBytes(b), mod, key, nonce).FillBytes(b)
}

// DecryptBytesField inverts EncryptBytesField.
func DecryptBytesField(b, key []byte, nonce uint64) {
	mod := new(big.Int).Lsh(big.NewInt(1), uint(8*len(b)))
	DecryptRank(new(big.Int).SetBytes(b), mod, key, nonce).FillBytes(b)
}
