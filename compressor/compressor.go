package compressor

import (
	"ajz/util"
	"math/big" // big "github.com/ncw/gmp"
	"math/bits"
	"sort"
	"sync"

	"github.com/bits-and-blooms/bitset"
)

// findContiguityLengths fills lengths with the lengths of the maximal runs of
// consecutive integers in nums (a run continues while each value is the previous
// plus one). It is the run detector for the optional hockey-stick ("contiguous
// runs") ranker, which sums a whole diagonal segment in closed form instead of term
// by term. nums is assumed ascending; lengths must hold one entry per run.
func findContiguityLengths(nums []uint, lengths []int) {
	bigL := len(nums)
	if bigL == 0 {
		return
	}
	startIdx := 0
	idx := 0
	prev := nums[0]
	for i := 1; i < bigL; i++ { // boundary check hoisted out of the loop
		cur := nums[i] // single index per iteration; prev avoids re-reading nums[i-1]
		if cur != prev+1 {
			lengths[idx] = i - startIdx
			startIdx = i
			idx++
		}
		prev = cur
	}
	lengths[idx] = bigL - startIdx // final run
}

// rank computes the colexicographic rank of indexSet.
// The first three terms are seeded with closed-form arithmetic (no BinCoef
// lookup). Remaining terms are accumulated in uint64 for as long as possible:
// once a term would overflow the accumulator (either because the term itself
// exceeds 64 bits, or because adding it to the running total would carry), the
// accumulator is promoted to big.Int and the rest of the terms are added there.
// Ranks that fit entirely in uint64 never touch big.Int at all.
func rank(indexSet []uint, result *big.Int) {
	k := len(indexSet)
	if k == 0 {
		result.SetUint64(0)
		return
	}
	if k == 1 {
		result.SetUint64(uint64(indexSet[0]))
		return
	}

	// Seed with the first three terms via closed-form arithmetic (no BinCoef
	// lookup). Safe for any block size up to 65535: the largest intermediate
	// product n*(n-1)*(n-2) at n=65534 is ~2.81e14, well below 2^64.
	n1 := uint64(indexSet[1])
	acc := uint64(indexSet[0]) + n1*(n1-1)/2
	if k == 2 {
		result.SetUint64(acc)
		return
	}
	n2 := uint64(indexSet[2])
	acc += n2 * (n2 - 1) * (n2 - 2) / 6

	for i := 3; i < k; i++ {
		bc := util.BinCoef(int(indexSet[i]), i+1)
		// Promote if the term itself overflows uint64 ...
		if bc.BitLength > 64 {
			result.SetUint64(acc)
			for ; i < k; i++ {
				result.Add(util.BinCoef(int(indexSet[i]), i+1).Value, result)
			}
			return
		}
		// ... or if adding it to the accumulator would overflow.
		term := bc.Value.Uint64()
		if _, carry := bits.Add64(acc, term, 0); carry != 0 {
			result.SetUint64(acc)
			result.Add(bc.Value, result)
			i++
			for ; i < k; i++ {
				result.Add(util.BinCoef(int(indexSet[i]), i+1).Value, result)
			}
			return
		}
		acc += term
	}

	// Entire rank fit in uint64.
	result.SetUint64(acc)
}

// rankHockeyStick computes the colexicographic rank of indexSet using the
// hockey-stick identity to reduce big.Int additions when consecutive positions
// form contiguous runs. For a run of length L starting at position p at level i,
// the identity collapses L individual binom(p+j, i+j) terms into a single
// binom(p+L, i+L) − binom(p, i) pair, saving L−2 additions per run of length
// ≥ 3. This is most effective when the input has many long contiguous runs (e.g.
// sorted or near-sorted position lists). contiguityLengths is a caller-supplied
// scratch buffer of length ≥ len(indexSet) used to avoid allocation.
func rankHockeyStick(indexSet []uint, result *big.Int, contiguityLengths []int) {
	length := len(indexSet)
	if length == 0 {
		result.SetUint64(0)
		return
	}
	result.SetUint64(uint64(indexSet[0]))
	i := 1
	iplus1 := 2
	iplus2 := 3
	if length > 1 {
		findContiguityLengths(indexSet[1:], contiguityLengths)
		for _, contiguityLength := range contiguityLengths {
			if i >= length {
				break
			} else if contiguityLength == 1 {
				result.Add(util.BinCoef(int(indexSet[i]), iplus1).Value, result)
			} else if contiguityLength == 2 {
				result.Add(util.BinCoef(int(indexSet[i]), iplus1).Value, result)
				result.Add(util.BinCoef(int(indexSet[iplus1]), iplus2).Value, result)
			} else {
				// Use hockey-stick identity to reduce additions
				result.Add(util.BinCoef(int(indexSet[i])+contiguityLength, i+contiguityLength).Value, result)
				result.Sub(result, util.BinCoef(int(indexSet[i]), i).Value)
			}
			i += contiguityLength
			iplus1 += contiguityLength
			iplus2 += contiguityLength
		}
	}
}

// blockScratch holds the per-block working buffers. One instance is reused
// across many calls to Process via scratchPool, avoiding a fresh round of
// allocations (and the resulting GC pressure) for every block.
type blockScratch struct {
	compressionIndex *big.Int
	alphabetBitSet   *bitset.BitSet
	bitWriter        *util.BitWriter
	counts           []int  // per-byte occurrence counts (len 256)
	offsets          []int  // per-byte start offsets into positions, prefix sums (len 257)
	cursor           []int  // per-byte fill cursors for the scatter pass (len 256)
	positions        []uint // all block positions, grouped by byte value (len >= block)
	toRemove         *bitset.BitSet
	indexSet         []uint // reusable buffer for the alphabet set
	sortedKeys       []byte
}

var scratchPool = sync.Pool{
	New: func() any {
		return &blockScratch{
			compressionIndex: big.NewInt(0),
			alphabetBitSet:   bitset.MustNew(256),
			bitWriter:        util.NewBitWriter(),
			counts:           make([]int, 256),
			offsets:          make([]int, 257),
			cursor:           make([]int, 256),
			toRemove:         bitset.MustNew(0),
			indexSet:         make([]uint, 0, 256),
			sortedKeys:       make([]byte, 0, 256),
		}
	},
}

// Process compresses one block and returns its packed bytes. It is safe for
// concurrent use: per-block working memory is taken from scratchPool, and
// referenceAlphabetBitSet is only read.
func Process(inputBytes []byte, referenceAlphabetBitSet *bitset.BitSet) []byte {
	return process(inputBytes, referenceAlphabetBitSet, nil)
}

// ProcessEncrypted compresses one block and encrypts the alphabet rank and each
// position rank in place over their exact moduli (the queryable dial setting:
// the alphabet cardinality, the most-frequent value, and the counts stay in
// clear, so the counts still form a skip-table). The output is byte-for-byte the
// same size as the unencrypted block.
func ProcessEncrypted(inputBytes []byte, referenceAlphabetBitSet *bitset.BitSet, cipher util.Cipher) []byte {
	return process(inputBytes, referenceAlphabetBitSet, &cipher)
}

// process is the per-block encoder behind Process / ProcessEncrypted. It turns one
// input block into its packed bytes, following the paper's pipeline (map -> reduce
// -> rank & pack). With a non-nil cipher it also encrypts fields in place over their
// exact moduli: the alphabet rank and position ranks always (the queryable dial),
// and the fixed-width header fields and counts too when cipher.Full is set.
//
// Stages:
//  1. Counting sort: group every position by its byte value into one backing array
//     (counts -> prefix-sum offsets -> scatter), which also yields the alphabet
//     (which byte values occur) and the most-frequent value.
//  2. Alphabet: XOR the block alphabet against the stream-global reference alphabet
//     (the stream-mode symmetric difference), then write its cardinality (9 bits)
//     and its colexicographic rank over C(256, cardinality).
//  3. Most-frequent value: written verbatim and ordered last, so it is recovered by
//     elimination rather than ranked (its omitted final factor is C(k,k)=1).
//  4. Per-value loop over every value except the most-frequent: reduce the value's
//     positions into the shrinking residual universe (ReduceIndexSet), rank the
//     reduced set over C(nPayload, k), and pack the count k (one shared width) and
//     the rank. nPayload shrinks by k each step, so the ranks telescope onto the
//     block's multinomial coefficient.
//
// Working memory is pooled (scratchPool); the returned slice is a fresh copy, since
// the writer's buffer is reused by the next block.
func process(inputBytes []byte, referenceAlphabetBitSet *bitset.BitSet, cipher *util.Cipher) []byte {
	numInputBytes := uint(len(inputBytes))
	numBitsForNumBytes := bits.Len(numInputBytes)

	s := scratchPool.Get().(*blockScratch)
	defer scratchPool.Put(s)

	compressionIndex := s.compressionIndex
	alphabetBitSet := s.alphabetBitSet
	alphabetBitSet.ClearAll()
	bitWriter := s.bitWriter
	bitWriter.Reset()

	// Counting-sort layout: group every position by its byte value inside a
	// single backing array, instead of 256 individually-growing append slices.
	// Pass 1 counts occurrences; the prefix sums give each byte its contiguous
	// region; pass 2 scatters positions into those regions in ascending order.
	counts := s.counts
	for i := range counts {
		counts[i] = 0
	}
	for _, b := range inputBytes {
		counts[b]++
	}
	offsets := s.offsets
	offsets[0] = 0
	for i := 0; i < 256; i++ {
		offsets[i+1] = offsets[i] + counts[i]
	}
	if cap(s.positions) < len(inputBytes) {
		s.positions = make([]uint, len(inputBytes))
	}
	positions := s.positions[:len(inputBytes)]
	cursor := s.cursor
	copy(cursor, offsets[:256])
	for i := uint(0); i < numInputBytes; i++ {
		b := inputBytes[i]
		positions[cursor[b]] = i // each region fills in ascending position order
		cursor[b]++
	}

	sortedKeys := s.sortedKeys[:0]
	mostFrequentByte := byte(0)
	freq := 0
	for i := range 256 {
		numPositions := counts[i]
		if numPositions > 0 {
			alphabetBitSet.Set(uint(i))
			sortedKeys = append(sortedKeys, byte(i))
			if numPositions > freq {
				mostFrequentByte = byte(i)
				freq = numPositions
			}
		}
	}

	// In place XOR.
	alphabetBitSet.InPlaceSymmetricDifference(referenceAlphabetBitSet)

	cardinality := alphabetBitSet.Count()
	cToWrite := uint64(cardinality)
	if cipher != nil && cipher.Full {
		cToWrite = util.EncryptUint(uint64(cardinality), 1<<9, cipher.Subkey,
			util.BlockFieldNonce(cipher.BlockIndex, util.FieldAlphabetCardinality))
	}
	bitWriter.WriteBits(cToWrite, 9)
	maxCompressionIndexBits := util.BinCoef(256, int(cardinality)).BitLength
	util.GrowBigInt(compressionIndex, uint(maxCompressionIndexBits))
	rank(util.GetIndexSetWithBuffer(alphabetBitSet, s.indexSet), compressionIndex)
	if cipher != nil {
		mod := util.BinCoef(256, int(cardinality)).Value
		compressionIndex.Set(util.EncryptRank(compressionIndex, mod, cipher.Subkey,
			util.BlockFieldNonce(cipher.BlockIndex, util.FieldAlphabetRank)))
	}
	bitWriter.WriteBigIntBits(compressionIndex, uint(maxCompressionIndexBits))

	mfbToWrite := mostFrequentByte
	if cipher != nil && cipher.Full {
		mfbToWrite = byte(util.EncryptUint(uint64(mostFrequentByte), 1<<8, cipher.Subkey,
			util.BlockFieldNonce(cipher.BlockIndex, util.FieldMostFrequent)))
	}
	bitWriter.WriteByte(mfbToWrite) // most-frequent byte (encrypted in full mode)

	sort.Slice(sortedKeys, func(i, j int) bool {
		if sortedKeys[i] == mostFrequentByte {
			return false
		}
		if sortedKeys[j] == mostFrequentByte {
			return true
		}
		return sortedKeys[i] < sortedKeys[j]
	})

	toRemove := s.toRemove
	toRemove.ClearAll()

	maxK := 0
	for i := 0; i < len(sortedKeys)-1; i++ {
		maxK = max(maxK, counts[sortedKeys[i]])
	}
	maxKBits := util.NumBitsRequiredToRepresentBigInt(uint(maxK))
	maxKBitsToWrite := uint64(maxKBits)
	if cipher != nil && cipher.Full {
		maxKBitsToWrite = util.EncryptUint(uint64(maxKBits), uint64(1)<<numBitsForNumBytes, cipher.Subkey,
			util.BlockFieldNonce(cipher.BlockIndex, util.FieldCountWidth))
	}
	bitWriter.WriteBits(maxKBitsToWrite, uint(numBitsForNumBytes))

	k := 0
	nPayload := numInputBytes

	for i := 0; i < len(sortedKeys)-1; i++ {
		key := int(sortedKeys[i]) // int, not byte: key+1 must not overflow at key==255
		pos := positions[offsets[key]:offsets[key+1]]
		util.ReduceIndexSet(pos, toRemove)

		k = len(pos)
		payloadBC := util.BinCoef(int(nPayload), k)
		maxPayloadBits := payloadBC.BitLength
		util.GrowBigInt(compressionIndex, uint(maxPayloadBits))
		rank(pos, compressionIndex)
		if cipher != nil {
			compressionIndex.Set(util.EncryptRank(compressionIndex, payloadBC.Value, cipher.Subkey,
				util.BlockFieldNonce(cipher.BlockIndex, util.SymbolRankField(i))))
		}

		kToWrite := uint64(k)
		if cipher != nil && cipher.Full {
			kToWrite = util.EncryptUint(uint64(k), uint64(1)<<maxKBits, cipher.Subkey,
				util.BlockFieldNonce(cipher.BlockIndex, util.SymbolCountField(i)))
		}
		bitWriter.WriteBits(kToWrite, maxKBits)
		bitWriter.WriteBigIntBits(compressionIndex, uint(maxPayloadBits))

		nPayload -= uint(k)
	}

	// Copy out: bitWriter.Bytes() aliases the pooled writer's buffer, which the
	// next block will Reset and overwrite, so hand the caller its own copy.
	src := bitWriter.Bytes()
	bs := make([]byte, len(src))
	copy(bs, src)
	return bs
}
