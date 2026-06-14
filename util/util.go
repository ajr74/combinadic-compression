package util

import (
	"fmt"
	"math/big" // big "github.com/ncw/gmp"
	"math/bits"
	"slices"

	"github.com/bits-and-blooms/bitset"
)

// Big integers:

// NumBitsRequiredToRepresentBigInt returns the number of bits needed to
// represent value, returning 1 when value is 0.
func NumBitsRequiredToRepresentBigInt(value uint) uint {
	numBits := bits.Len(value)
	if numBits == 0 {
		return 1
	}
	return uint(numBits)
}

// GrowBigInt ensures z has backing capacity for at least nBits bits without
// changing its value. Pre-sizing before a sequence of Add/Sub operations that
// build up to a value of known magnitude (e.g. a rank bounded by a binomial
// coefficient) avoids repeated reallocation of the internal nat slice as the
// accumulator grows word by word.
//
// math/big exposes no Grow method, so we force the allocation by setting and
// then clearing a bit at position nBits: the set extends the slice, and the
// clear's norm() trims the length back while retaining the slice capacity.
func GrowBigInt(z *big.Int, nBits uint) {
	if uint(z.BitLen()) >= nBits {
		return // capacity already covers nBits bits
	}
	z.SetBit(z, int(nBits), 1)
	z.SetBit(z, int(nBits), 0)
}

// Bitsets:

// GetIndexSet returns the indices of the set bits of value, in ascending order.
func GetIndexSet(value *bitset.BitSet) []uint { // TODO (i) re-use buffer, (ii) investigate AsSlice
	indices := make([]uint, value.Count())
	value.NextSetMany(0, indices)
	return indices
}

// GetIndexSetWithBuffer returns the indices of the set bits of value, in
// ascending order, written into buffer (whose capacity must be at least
// value.Count()). The returned slice aliases buffer.
func GetIndexSetWithBuffer(value *bitset.BitSet, buffer []uint) []uint { // TODO (i) re-use buffer, (ii) investigate AsSlice
	//_, result := value.NextSetMany(0, buffer)
	//return result
	return value.AsSlice(buffer)
}

func uintToBitSet(value uint, length int) bitset.BitSet {
	return StringToBitSet(fmt.Sprintf("%0*b", length, value))
}

// StringToBitSet builds a len(s)-bit set from a string of '0'/'1' characters,
// setting bit i iff s[i] == '1'.
func StringToBitSet(s string) bitset.BitSet {
	b := bitset.MustNew(uint(len(s)))
	for i := 0; i < len(s); i++ {
		b.SetTo(uint(i), s[i] == '1')
	}
	return *b
}

// ReduceIndexSet rewrites each element of indexes (which must be sorted
// ascending) to its position in the space that excludes the already-occupied
// positions in toRemove: each index is decreased by the number of set bits in
// toRemove below it. It also marks every element of indexes in toRemove, so
// successive calls with disjoint, increasing sets compose to exclude all
// previously placed positions.
func ReduceIndexSet(indexes []uint, toRemove *bitset.BitSet) {
	cardinality := uint(0)
	start := uint(0)
	for i := 0; i < len(indexes); i++ {
		toRemove.Set(indexes[i])
		cardinality += toRemove.OnesBetween(start, indexes[i])
		start = indexes[i] + 1
		indexes[i] -= cardinality
	}
}

// IndexSetToBitSetString renders indexes as a length-bit row in the LaTeX
// \drawbits box notation used by the documentation.
func IndexSetToBitSetString(indexes []uint, length uint) string {
	s := "&a:\\drawbits{"
	for i := uint(0); i < length; i++ {
		if slices.Contains(indexes, i) {
			s += "1"
		} else {
			s += "0"
		}
		if i < length-1 {
			s += ", "
		}
	}
	s += "} \\\\"
	return s
}

// BitSetFromIndexSet returns a length-bit set with exactly the bits in indexes
// set.
func BitSetFromIndexSet(indexes []uint, length uint) bitset.BitSet {
	bs := bitset.MustNew(length)
	for i := 0; i < len(indexes); i++ {
		bs.Set(indexes[i])
	}
	// TODO any advantage to descending traverse?
	return *bs
}

// Bit writer (allocation-free):

// BitWriter packs bits MSB-first into a byte buffer, without allocating a
// temporary BitArray per field the way the go-bitarray Builder path does. The
// first bit written becomes the most-significant bit of the first output byte,
// which reproduces exactly the byte stream the decompressor's bitarray reader
// expects. A BitWriter is reusable across blocks via Reset.
type BitWriter struct {
	buf  []byte
	acc  uint64 // pending bits, held right-aligned in the low nAcc bits
	nAcc uint   // number of valid pending bits (0..7 between writes)
}

// NewBitWriter returns an empty BitWriter ready for use.
func NewBitWriter() *BitWriter { return &BitWriter{} }

// Reset clears the writer for reuse, retaining the buffer's capacity.
func (w *BitWriter) Reset() {
	w.buf = w.buf[:0]
	w.acc = 0
	w.nAcc = 0
}

// WriteBits appends the low n bits of v (n <= 56), most-significant bit first.
func (w *BitWriter) WriteBits(v uint64, n uint) {
	if n == 0 {
		return
	}
	v &= (uint64(1) << n) - 1
	w.acc = (w.acc << n) | v
	w.nAcc += n
	for w.nAcc >= 8 {
		w.nAcc -= 8
		w.buf = append(w.buf, byte(w.acc>>w.nAcc))
	}
	w.acc &= (uint64(1) << w.nAcc) - 1 // drop already-emitted high bits
}

// WriteByte appends a full byte (8 bits), MSB first. It returns a nil error so
// that BitWriter satisfies io.ByteWriter.
func (w *BitWriter) WriteByte(b byte) error {
	w.WriteBits(uint64(b), 8)
	return nil
}

// WriteZeros appends n zero bits.
func (w *BitWriter) WriteZeros(n uint) {
	for n >= 32 {
		w.WriteBits(0, 32)
		n -= 32
	}
	if n > 0 {
		w.WriteBits(0, n)
	}
}

// WriteBigIntBits appends value as exactly length bits, MSB first, left-padded
// with zeros. This matches the old bitarray path
// NewFromBytes(value.Bytes(), 0, ...).ToWidth(length, AlignRight). It requires
// 0 <= value < 2^length (i.e. length >= value.BitLen()), which every caller
// guarantees because each rank is strictly bounded by its binomial coefficient
// and length is that coefficient's bit length.
func (w *BitWriter) WriteBigIntBits(value *big.Int, length uint) {
	actualBits := uint(value.BitLen())
	if actualBits < length {
		w.WriteZeros(length - actualBits)
	}
	if actualBits == 0 {
		return
	}
	vb := value.Bytes() // big-endian, minimal length
	topBits := actualBits - uint(len(vb)-1)*8
	w.WriteBits(uint64(vb[0]), topBits)
	for i := 1; i < len(vb); i++ {
		w.WriteBits(uint64(vb[i]), 8)
	}
}

// Bytes flushes any partial final byte (zero-padded in the low bits, exactly as
// the bitarray Builder did) and returns the packed buffer. The returned slice
// aliases the writer's internal buffer, so copy it if it must outlive the next
// Reset.
func (w *BitWriter) Bytes() []byte {
	if w.nAcc > 0 {
		w.buf = append(w.buf, byte(w.acc<<(8-w.nAcc)))
		w.acc = 0
		w.nAcc = 0
	}
	return w.buf
}

// Bit reader (allocation-free):

// BitReader reads bits MSB-first from a byte slice — the inverse of BitWriter
// and matching the compressor's encoding. Fields are consumed in sequence via an
// advancing bit position, so no temporary BitArray is allocated per field. A
// BitReader is reusable across blocks via Reset.
type BitReader struct {
	data []byte
	pos  uint // next bit position to read
}

// NewBitReader returns a BitReader positioned at the start of data.
func NewBitReader(data []byte) *BitReader { return &BitReader{data: data} }

// Reset re-points the reader at data and rewinds to the start, for reuse.
func (r *BitReader) Reset(data []byte) {
	r.data = data
	r.pos = 0
}

// ReadBits reads the next n bits (n <= 56), MSB-first, as a uint64.
func (r *BitReader) ReadBits(n uint) uint64 {
	var v uint64
	for n > 0 {
		bitOff := r.pos & 7
		avail := 8 - bitOff
		take := avail
		if take > n {
			take = n
		}
		shift := avail - take
		chunk := (uint64(r.data[r.pos>>3]) >> shift) & ((uint64(1) << take) - 1)
		v = (v << take) | chunk
		r.pos += take
		n -= take
	}
	return v
}

// ReadByte reads the next 8 bits as a byte. It returns a nil error so that
// BitReader satisfies io.ByteReader.
func (r *BitReader) ReadByte() (byte, error) { return byte(r.ReadBits(8)), nil }

// ReadBigInt reads the next n bits, MSB-first, into result. scratch is a reusable
// byte buffer grown as needed to avoid per-call allocation; the (possibly grown)
// slice is returned so the caller can retain its backing array.
func (r *BitReader) ReadBigInt(n uint, result *big.Int, scratch []byte) []byte {
	if n == 0 {
		result.SetUint64(0)
		return scratch
	}
	nBytes := int((n + 7) >> 3)
	if cap(scratch) < nBytes {
		scratch = make([]byte, nBytes)
	}
	buf := scratch[:nBytes]
	topBits := n - uint(nBytes-1)*8 // 1..8 significant bits in the leading byte
	buf[0] = byte(r.ReadBits(topBits))
	for i := 1; i < nBytes; i++ {
		buf[i] = byte(r.ReadBits(8))
	}
	result.SetBytes(buf)
	return scratch
}
