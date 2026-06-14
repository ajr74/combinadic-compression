package util

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"

	"golang.org/x/crypto/chacha20"
)

// A keystream generator produces n pseudorandom bytes deterministically from a key
// and a uint64 nonce; rankKeystream reduces those bytes modulo the field modulus.
//
// The default generator --- and the one behind every reported measurement, including
// the throughput table of Section sec:encexp --- is SHA-256 in counter mode: a clear
// reference, chosen for legibility rather than speed. AES-256-CTR and ChaCha20 are
// provided as validated drop-in alternatives (keystream_test.go checks all three give
// the identical rank cipher), so the construction is demonstrably keystream-agnostic;
// a production build would prefer one of them for speed (AES uses hardware AES
// instructions where present; ChaCha20 is the portable, constant-time software
// alternative). Neither is selected by the CLI --- only via SetKeystream.
//
// NOTE: SetKeystream mutates package-level state and is therefore not safe to call
// concurrently with encryption. It is intended for benchmarks and tests, which set
// it serially.
type keystreamGen func(key []byte, nonce uint64, n int) []byte

var keystreamSource keystreamGen = sha256Keystream

// SetKeystream selects the keystream generator by name: "sha256" (the default
// reference), "aes" (AES-256-CTR), or "chacha20". An unrecognised name selects the
// default.
func SetKeystream(name string) {
	switch name {
	case "aes":
		keystreamSource = aesCTRKeystream
	case "chacha20":
		keystreamSource = chacha20Keystream
	default:
		keystreamSource = sha256Keystream
	}
}

// normaliseKey returns a 32-byte key for the block ciphers (AES-256, ChaCha20),
// hashing the input when it is not already 32 bytes.
func normaliseKey(key []byte) []byte {
	if len(key) == 32 {
		return key
	}
	s := sha256.Sum256(key)
	return s[:]
}

// sha256Keystream is the reference generator: SHA-256(key || nonce || counter),
// concatenated until n bytes are available.
func sha256Keystream(key []byte, nonce uint64, n int) []byte {
	var nb [8]byte
	binary.BigEndian.PutUint64(nb[:], nonce)
	buf := make([]byte, 0, ((n+31)/32)*32)
	for ctr := uint32(0); len(buf) < n; ctr++ {
		h := sha256.New()
		h.Write(key)
		h.Write(nb[:])
		var cb [4]byte
		binary.BigEndian.PutUint32(cb[:], ctr)
		h.Write(cb[:])
		buf = h.Sum(buf)
	}
	return buf[:n]
}

// aesCTRKeystream generates n bytes of AES-256-CTR keystream (the IV carries the
// nonce in its high 8 bytes).
func aesCTRKeystream(key []byte, nonce uint64, n int) []byte {
	block, err := aes.NewCipher(normaliseKey(key))
	if err != nil {
		panic(err) // a 32-byte key is always valid for AES-256
	}
	var iv [aes.BlockSize]byte
	binary.BigEndian.PutUint64(iv[:8], nonce)
	out := make([]byte, n)
	cipher.NewCTR(block, iv[:]).XORKeyStream(out, out)
	return out
}

// chacha20Keystream generates n bytes of ChaCha20 keystream. The raw
// (unauthenticated) cipher is the correct primitive here: this scheme needs a
// keystream, and authentication is a separate per-block concern (Section
// sec:encryption).
func chacha20Keystream(key []byte, nonce uint64, n int) []byte {
	var nb [chacha20.NonceSize]byte
	binary.BigEndian.PutUint64(nb[chacha20.NonceSize-8:], nonce)
	c, err := chacha20.NewUnauthenticatedCipher(normaliseKey(key), nb[:])
	if err != nil {
		panic(err) // a 32-byte key and 12-byte nonce are always valid
	}
	out := make([]byte, n)
	c.XORKeyStream(out, out)
	return out
}
