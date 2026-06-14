package util

import (
	"crypto/rand"
	"crypto/sha256"
)

// Nonce and field-index contract for the size-preserving block cipher.
//
// The cipher in cipher.go encrypts one integer field under a (key, nonce) pair,
// and REQUIRES a distinct nonce for every field encrypted under a given key
// (reuse degenerates to a two-time pad). This file fixes the shared agreement
// between encryptor and decryptor that supplies those nonces and makes any block
// independently decryptable (across-block random access under full encryption).
//
// Field enumeration (decode order within a block; see compression.tex):
//
//	0  alphabet cardinality c
//	1  alphabet rank        (over C(256,c))
//	2  most-frequent value
//	3  count-width header   (the uniform per-count field width; full mode only)
//	4  count  k_0           ─┐ for each of the c-1 transmitted symbols j,
//	5  rank   r_0            │ count then rank, interleaved:
//	6  count  k_1            │   count field = 4 + 2j
//	7  rank   r_1            │   rank  field = 4 + 2j + 1
//	...                     ─┘
//
// The final (most-frequent) value carries no rank; it is recovered by
// elimination. With c <= 256 the largest field index is 4 + 2*254 + 1 = 513.
//
// Nonce layout (uint64), most-significant to least:
//
//	[ domain : 16 ][ block index : 32 ][ field index : 16 ]
//
// domain 0 is per-block fields; domain 1 is the stream-global alphabet
// (encrypted once per stream). Because a block index occupies bits 16..47, no
// per-block nonce ever reaches the domain-1 value (1<<48), so the alphabet can
// never collide with a block field. Field indices fit in 16 bits (max 512), and
// block indices in 32 bits, so every (block, field) pair maps to a distinct
// nonce within a file.
//
// Per-file separation. The same master key applied to two files must not reuse
// keystreams, so the cipher key is a per-file subkey derived from the master key
// and a random file salt (stored in the header); identical nonces across files
// are then harmless because the subkey differs.
//
// SECURITY NOTE. Like cipher.go this is a reference contract: DeriveFileKey uses
// SHA-256 rather than a standardised KDF (HKDF) and the keystream is SHA-256 in
// counter mode; a production build should use HKDF and a vetted stream cipher.

// Canonical field indices for a block's header fields.
const (
	FieldAlphabetCardinality uint16 = 0
	FieldAlphabetRank        uint16 = 1
	FieldMostFrequent        uint16 = 2
	FieldCountWidth          uint16 = 3
	fieldFirstSymbol         uint16 = 4
)

const (
	nonceFieldBits                      = 16
	nonceBlockBits                      = 32
	nonceDomainBlock             uint64 = 0
	nonceDomainReferenceAlphabet uint64 = 1
	nonceDomainHash              uint64 = 2
)

// HashNonce is the nonce for the per-file integrity hash, in its own domain so it
// never collides with a block field or the alphabet.
func HashNonce() uint64 {
	return nonceDomainHash << (nonceBlockBits + nonceFieldBits)
}

// SymbolCountField returns the field index of the count k_j of the j-th
// transmitted symbol (0-based, in encoder processing order).
func SymbolCountField(j int) uint16 { return fieldFirstSymbol + uint16(2*j) }

// SymbolRankField returns the field index of the rank r_j of the j-th
// transmitted symbol, which immediately follows its count.
func SymbolRankField(j int) uint16 { return fieldFirstSymbol + uint16(2*j) + 1 }

// BlockFieldNonce maps a (block index, field index) pair to its unique cipher
// nonce within a file.
func BlockFieldNonce(blockIndex uint32, fieldIndex uint16) uint64 {
	return (nonceDomainBlock << (nonceBlockBits + nonceFieldBits)) |
		(uint64(blockIndex) << nonceFieldBits) |
		uint64(fieldIndex)
}

// ReferenceAlphabetNonce is the nonce for the stream-global alphabet, encrypted
// once per stream in its own domain so it never collides with a block field.
func ReferenceAlphabetNonce() uint64 {
	return nonceDomainReferenceAlphabet << (nonceBlockBits + nonceFieldBits)
}

// FileSaltLen is the length in bytes of the per-file salt stored in the header.
const FileSaltLen = 16

// NewFileSalt returns a fresh random per-file salt.
func NewFileSalt() ([]byte, error) {
	b := make([]byte, FileSaltLen)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

// DeriveFileKey derives the per-file cipher subkey from the master key and the
// file salt. (Reference derivation; production should use HKDF.)
func DeriveFileKey(masterKey, fileSalt []byte) []byte {
	h := sha256.New()
	h.Write(fileSalt)
	h.Write(masterKey)
	return h.Sum(nil)
}

// Cipher carries the per-file subkey and the block index that together supply
// the (key, nonce) pairs for the size-preserving field cipher. It is passed to
// the encrypting compress/decompress paths; the nonce for a field is
// BlockFieldNonce(BlockIndex, fieldIndex).
type Cipher struct {
	Subkey     []byte
	BlockIndex uint32
	// Full selects full-block encryption (every field) when true, or the
	// queryable dial setting (alphabet rank and position ranks only, counts and
	// cardinality left in clear) when false.
	Full bool
}
