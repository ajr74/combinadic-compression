package decompressor

import (
	"ajz/util"
	"math/big" // big "github.com/ncw/gmp"
	"math/bits"
	"sort"
	"sync"

	"github.com/bits-and-blooms/bitset"
)

// unrank is the inverse of the colexicographic rank --- the decode side of the
// combinatorial number system (the "combinadic"). Given a rank `index`, a
// population count `kVal`, and a universe of `numBits` positions, it reconstructs
// the kVal "on" positions of the ranked subset into `result`. It inverts
// r = sum_i C(p_i, i) by the standard greedy descent: from the most-significant
// position down, repeatedly take the largest position j whose binomial coefficient
// does not exceed the running remainder, set it, and subtract that coefficient ---
// the unranking step of the decompression algorithm.
//
// result is cleared on entry; the caller sizes it to the universe (a 256-bit set
// here, so numBits <= 256). Two shortcuts keep it cheap:
//   - magnitudes are screened by cached bit length first (BinCoef carries it), so
//     the expensive big.Int comparison is reached only on a tie in bit length;
//   - the weight-1 position needs no scan: C(j,1) = j, so whatever remainder is
//     left after placing the higher weights is exactly that final position.
func unrank(index *big.Int, kVal uint, numBits uint, result *bitset.BitSet) {
	// Method assumes len(result) >= kVal

	result.ClearAll()
	if kVal == 0 {
		return
	}

	start := numBits - 1
	for i := kVal; i > 1; i-- {
		indexBitLength := uint16(index.BitLen())
		for j := start; int(j) >= 0; j-- {
			bc := util.BinCoef(int(j), int(i)) // N > k on average
			if bc.BitLength < indexBitLength || (bc.BitLength == indexBitLength && bc.Value.Cmp(index) <= 0) {
				result.Set(j)
				start = j - 1
				index.Sub(index, bc.Value)
				break
			}
		}
	}
	result.Set(uint(index.Int64()))
}

// blockScratch holds the per-block working buffers for Process. One instance
// is reused across many blocks via scratchPool, avoiding a fresh round of
// allocations (and the resulting GC pressure) for every block.
type blockScratch struct {
	reader           *util.BitReader
	compressionIndex *big.Int
	bigScratch       []byte         // reusable buffer for BitReader.ReadBigInt
	alphabetBitSet   *bitset.BitSet // 256-bit alphabet bitset
	reducedBitSet    *bitset.BitSet // sized >= numBlockBytes
	unoccupied       *bitset.BitSet // sized >= numBlockBytes
	posBuffer        []uint         // >= numBlockBytes, enumerates unoccupied positions
	keyBuffer        []uint         // cap 256, the sorted alphabet keys
}

var scratchPool = sync.Pool{
	New: func() any {
		return &blockScratch{
			reader:           util.NewBitReader(nil),
			compressionIndex: big.NewInt(0),
			alphabetBitSet:   bitset.MustNew(256),
			reducedBitSet:    bitset.MustNew(0),
			unoccupied:       bitset.MustNew(0),
			keyBuffer:        make([]uint, 0, 256),
		}
	},
}

// Process decompresses one block of inputBytes into result, which must have
// length numBlockBytes. It is safe for concurrent use: per-block working
// memory comes from scratchPool and referenceAlphabetBitSet is only read.
func Process(inputBytes []byte, numBlockBytes uint, referenceAlphabetBitSet *bitset.BitSet, result []byte) {
	process(inputBytes, numBlockBytes, referenceAlphabetBitSet, result, nil)
}

// ProcessEncrypted decompresses one block whose alphabet rank and position ranks
// were encrypted by compressor.ProcessEncrypted, decrypting each in place before
// unranking. The cipher must carry the same subkey and block index used to
// encrypt.
func ProcessEncrypted(inputBytes []byte, numBlockBytes uint, referenceAlphabetBitSet *bitset.BitSet, result []byte, cipher util.Cipher) {
	process(inputBytes, numBlockBytes, referenceAlphabetBitSet, result, &cipher)
}

// process is the per-block decoder behind Process / ProcessEncrypted. It reverses
// the encoder field by field, reconstructing one block into result (length
// numBlockBytes). With a non-nil cipher each field is decrypted in place before use,
// mirroring what compressor.process encrypted; in full mode a wrong key is caught by
// out-of-range cardinality / count checks rather than decoding to garbage.
//
// Stages (the inverse of the encoder):
//  1. Alphabet: read the cardinality (9 bits) and the alphabet rank, unrank it over
//     the 256 byte values, then XOR against the reference alphabet to recover the
//     block's actual alphabet.
//  2. Read the most-frequent value and the shared count width, and order the
//     alphabet as the encoder did (most-frequent value last).
//  3. Per-value loop. For every value except the last, read its count k and rank and
//     unrank the rank into a reduced bitset over the residual universe of nPayload
//     unfilled positions, then shrink nPayload by k. The final (most-frequent) value
//     is inferred: it takes all positions still unfilled, with no stored rank.
//  4. Rehydrate (within each iteration): the reduced bitset is in residual
//     coordinates, so walk the still-unoccupied positions in order and assign the
//     value to those the reduced bitset selects, marking them occupied so later
//     values see the same shrinking universe the encoder did.
//
// Working memory is pooled (scratchPool); referenceAlphabetBitSet is only read.
func process(inputBytes []byte, numBlockBytes uint, referenceAlphabetBitSet *bitset.BitSet, result []byte, cipher *util.Cipher) {
	s := scratchPool.Get().(*blockScratch)
	defer scratchPool.Put(s)

	numBitsForNumBlockBytes := uint(bits.Len(numBlockBytes))
	br := s.reader
	br.Reset(inputBytes)

	compressionIndex := s.compressionIndex
	alphabetBitset := s.alphabetBitSet
	if s.reducedBitSet.Len() < numBlockBytes {
		s.reducedBitSet = bitset.MustNew(numBlockBytes)
	}
	if s.unoccupied.Len() < numBlockBytes {
		s.unoccupied = bitset.MustNew(numBlockBytes)
	}
	if cap(s.posBuffer) < int(numBlockBytes) {
		s.posBuffer = make([]uint, numBlockBytes)
	}
	reducedBitSet := s.reducedBitSet
	unoccupiedPositions := s.unoccupied
	buffer := s.posBuffer[:numBlockBytes]

	cardinality := uint(br.ReadBits(9))
	if cipher != nil && cipher.Full {
		cardinality = uint(util.DecryptUint(uint64(cardinality), 1<<9, cipher.Subkey,
			util.BlockFieldNonce(cipher.BlockIndex, util.FieldAlphabetCardinality)))
		if cardinality > 256 {
			return // wrong key / corrupted: out-of-range cardinality
		}
	}
	maxCompressionIndexBits := util.BinCoef(256, int(cardinality)).BitLength
	s.bigScratch = br.ReadBigInt(uint(maxCompressionIndexBits), compressionIndex, s.bigScratch)
	if cipher != nil {
		mod := util.BinCoef(256, int(cardinality)).Value
		compressionIndex.Set(util.DecryptRank(compressionIndex, mod, cipher.Subkey,
			util.BlockFieldNonce(cipher.BlockIndex, util.FieldAlphabetRank)))
	}
	unrank(compressionIndex, cardinality, 256, alphabetBitset)

	// In place XOR.
	alphabetBitset.InPlaceSymmetricDifference(referenceAlphabetBitSet)

	mostFrequentByte, _ := br.ReadByte()
	maxKBits := uint(br.ReadBits(numBitsForNumBlockBytes))
	if cipher != nil && cipher.Full {
		mostFrequentByte = byte(util.DecryptUint(uint64(mostFrequentByte), 1<<8, cipher.Subkey,
			util.BlockFieldNonce(cipher.BlockIndex, util.FieldMostFrequent)))
		maxKBits = uint(util.DecryptUint(uint64(maxKBits), uint64(1)<<numBitsForNumBlockBytes, cipher.Subkey,
			util.BlockFieldNonce(cipher.BlockIndex, util.FieldCountWidth)))
	}

	sortedKeys := util.GetIndexSetWithBuffer(alphabetBitset, s.keyBuffer)
	sort.Slice(sortedKeys, func(i, j int) bool {
		if byte(sortedKeys[i]) == mostFrequentByte {
			return false
		}
		if byte(sortedKeys[j]) == mostFrequentByte {
			return true
		}
		return sortedKeys[i] < sortedKeys[j]
	})

	// Prepare unoccupied positions: exactly the first numBlockBytes bits set
	// (the pooled bitset may be larger, so clear any tail beyond the block).
	unoccupiedPositions.SetAll()
	for b := numBlockBytes; b < unoccupiedPositions.Len(); b++ {
		unoccupiedPositions.Clear(b)
	}

	nPayload, k := numBlockBytes, uint(0)
	var numByteAssignments, reducedBitSetCount uint

	for i := 0; i < len(sortedKeys); i++ {

		// Get the reduced bitset from the compression index
		if i < len(sortedKeys)-1 {
			k = uint(br.ReadBits(maxKBits))
			if cipher != nil && cipher.Full {
				k = uint(util.DecryptUint(uint64(k), uint64(1)<<maxKBits, cipher.Subkey,
					util.BlockFieldNonce(cipher.BlockIndex, util.SymbolCountField(i))))
				if k > nPayload {
					return // wrong key / corrupted: out-of-range count
				}
			}
			payloadBC := util.BinCoef(int(nPayload), int(k))
			maxCompressionIndexBits = payloadBC.BitLength
			s.bigScratch = br.ReadBigInt(uint(maxCompressionIndexBits), compressionIndex, s.bigScratch)
			if cipher != nil {
				compressionIndex.Set(util.DecryptRank(compressionIndex, payloadBC.Value, cipher.Subkey,
					util.BlockFieldNonce(cipher.BlockIndex, util.SymbolRankField(i))))
			}
			unrank(compressionIndex, k, nPayload, reducedBitSet)
			reducedBitSetCount = k
			nPayload -= k
		} else {
			reducedBitSet.SetAll()
			reducedBitSetCount = nPayload
		}

		// Rehydrate the reduced bitset and populate the result slice
		numByteAssignments = 0
		for p, q := range util.GetIndexSetWithBuffer(unoccupiedPositions, buffer) {
			if reducedBitSet.Test(uint(p)) {
				numByteAssignments++
				result[q] = byte(sortedKeys[i])
				unoccupiedPositions.Clear(q)
				if numByteAssignments == reducedBitSetCount {
					break
				}
			}
		}
	}
}
