package util

import (
	"bytes"
	"fmt"
	"testing"
)

// TestBlockFieldNonceNoCollision: every (block, field) pair, and the alphabet
// nonce, must map to a distinct value. A collision would be a two-time pad.
func TestBlockFieldNonceNoCollision(t *testing.T) {
	seen := map[uint64]string{}
	add := func(n uint64, what string) {
		if prev, ok := seen[n]; ok {
			t.Fatalf("nonce collision at %d: %q vs %q", n, prev, what)
		}
		seen[n] = what
	}

	blocks := []uint32{0, 1, 2, 41, 42, 1000, 1 << 20, 1<<32 - 1}
	fields := []uint16{
		FieldAlphabetCardinality, FieldAlphabetRank, FieldMostFrequent,
		SymbolCountField(0), SymbolRankField(0),
		SymbolCountField(100), SymbolRankField(100),
		SymbolCountField(254), SymbolRankField(254),
	}
	for _, blk := range blocks {
		for _, f := range fields {
			add(BlockFieldNonce(blk, f), fmt.Sprintf("block=%d field=%d", blk, f))
		}
	}
	add(ReferenceAlphabetNonce(), "alphabet")
}

// TestAlphabetNonceOutOfBlockRange: the alphabet nonce must lie above every
// possible per-block nonce (block index occupies bits 16..47).
func TestAlphabetNonceOutOfBlockRange(t *testing.T) {
	maxBlockNonce := BlockFieldNonce(^uint32(0), ^uint16(0))
	if ReferenceAlphabetNonce() <= maxBlockNonce {
		t.Fatalf("alphabet nonce %d not above max block nonce %d", ReferenceAlphabetNonce(), maxBlockNonce)
	}
}

// TestFieldEnumerationDistinct: header and per-symbol field indices never
// overlap, count precedes rank, and indices stay in range.
func TestFieldEnumerationDistinct(t *testing.T) {
	seen := map[uint16]string{
		FieldAlphabetCardinality: "cardinality",
		FieldAlphabetRank:        "alphabet-rank",
		FieldMostFrequent:        "most-frequent",
		FieldCountWidth:          "count-width",
	}
	if len(seen) != 4 {
		t.Fatalf("header field indices not distinct: %v", seen)
	}
	for j := 0; j < 255; j++ { // up to c-1 = 255 transmitted symbols
		cf, rf := SymbolCountField(j), SymbolRankField(j)
		if cf >= rf {
			t.Fatalf("symbol %d: count field %d should precede rank field %d", j, cf, rf)
		}
		for _, f := range []uint16{cf, rf} {
			if prev, ok := seen[f]; ok {
				t.Fatalf("field index %d reused (symbol %d vs %s)", f, j, prev)
			}
			seen[f] = fmt.Sprintf("symbol-%d", j)
		}
	}
}

// TestDeriveFileKey: deterministic for fixed inputs, sensitive to both the salt
// and the master key, and 32 bytes long.
func TestDeriveFileKey(t *testing.T) {
	key := []byte("master-key")
	salt1 := []byte("0123456789abcdef")
	salt2 := []byte("fedcba9876543210")

	k1 := DeriveFileKey(key, salt1)
	if len(k1) != 32 {
		t.Fatalf("subkey length %d, want 32", len(k1))
	}
	if !bytes.Equal(k1, DeriveFileKey(key, salt1)) {
		t.Fatal("DeriveFileKey not deterministic")
	}
	if bytes.Equal(k1, DeriveFileKey(key, salt2)) {
		t.Fatal("different salt produced the same subkey")
	}
	if bytes.Equal(k1, DeriveFileKey([]byte("other-key"), salt1)) {
		t.Fatal("different master key produced the same subkey")
	}
}

// TestNewFileSalt: correct length and not obviously repeating.
func TestNewFileSalt(t *testing.T) {
	a, err := NewFileSalt()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != FileSaltLen {
		t.Fatalf("salt length %d, want %d", len(a), FileSaltLen)
	}
	b, err := NewFileSalt()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("two successive salts were identical")
	}
}
