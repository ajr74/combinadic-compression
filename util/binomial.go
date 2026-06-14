package util

import (
	"fmt"
	"io"
	"math/big" // big "github.com/ncw/gmp"
	"os"
	"time"

	"github.com/schollz/progressbar/v3"
)

// Quiet, when set, suppresses InitCache's progress bar and informational output.
// main sets it from the -q flag. It defaults to false so other callers (tests,
// benchmarks) are unaffected.
var Quiet = false

var zero = ExtendedBigInt{big.NewInt(0), 0}

var one = ExtendedBigInt{big.NewInt(1), 1}

// Hit, Miss, LargestN, and LargestK are optional instrumentation counters for
// the BinCoef cache: cache hit/miss tallies and the largest n and k arguments
// observed. They are only updated when the corresponding code in BinCoef is
// enabled.
var Hit, Miss, LargestN, LargestK = uint64(0), uint64(0), uint64(0), uint64(0)

// halfDiagonals stores the cached binomial coefficients as the half-diagonals of
// Pascal's triangle (each row is a diagonal), populated by InitCache and indexed
// by BinCoef.
var halfDiagonals [][]ExtendedBigInt

const estimateCacheSize = false

// ExtendedBigInt pairs an arbitrary-precision integer with its cached bit
// length, letting callers size bit fields and compare magnitudes cheaply
// without recomputing Value.BitLen() each time.
type ExtendedBigInt struct {
	Value     *big.Int // the coefficient value
	BitLength uint16   // bit length of Value, i.e. Value.BitLen(). Sufficient for any
	// block InitCache will accept: it caps maxN at 65000, below the point where a
	// central coefficient's bit length would exceed 65535.
}

// InitCache precomputes the binomial coefficients for arguments up to maxN and
// stores them as the half-diagonals of Pascal's triangle, so that subsequent
// BinCoef calls are constant-time lookups. It must be called before any BinCoef
// call, and may be re-invoked to resize the cache (the previous contents are
// discarded).
func InitCache(maxN int) { // TODO add maxK ?
	// The cached bit lengths live in a uint16 (ExtendedBigInt.BitLength). The
	// largest cached coefficient is the central C(maxN, maxN/2), whose bit length
	// is ~maxN, so blocks approaching 2^16 would silently overflow that field
	// and corrupt every field width derived from it. In practice the cache's
	// quadratic memory cost makes blocks anywhere near this bound infeasible long
	// before correctness is at stake, but we guard explicitly so the failure is a
	// clear panic rather than silent corruption. The bound is conservative: it
	// rejects the top sliver of the 16-bit block range, all of which is
	// memory-infeasible anyway.
	if maxN > 65000 {
		panic(fmt.Sprintf("InitCache: maxN=%d too large; the central coefficient's "+
			"bit length would overflow the uint16 ExtendedBigInt.BitLength field "+
			"(and the cache would be infeasibly large)", maxN))
	}
	startTime := time.Now()
	maxNCopy := maxN
	maxN += 2

	halfDiagonals = make([][]ExtendedBigInt, maxNCopy>>1) // TODO replace with ~min(maxNCopy>>1, kMax)
	halfDiagonals[0] = make([]ExtendedBigInt, maxNCopy)

	barWriter := io.Writer(os.Stderr)
	if Quiet {
		barWriter = io.Discard
	}
	bar := progressbar.NewOptions(len(halfDiagonals),
		progressbar.OptionSetWriter(barWriter),
		//progressbar.OptionShowCount(),
		progressbar.OptionSetDescription("Caching (N,k) values: "),
		progressbar.OptionShowBytes(false),
		progressbar.OptionShowElapsedTimeOnFinish(),
		progressbar.OptionThrottle(10*time.Millisecond),
		progressbar.OptionClearOnFinish(),
		progressbar.OptionSetPredictTime(true))

	for i := 0; i < len(halfDiagonals[0]); i++ {
		count := big.NewInt(int64(i + 2))
		halfDiagonals[0][i] = ExtendedBigInt{count, uint16(count.BitLen())}
	}
	bar.Add(1)
	for i := 1; i < len(halfDiagonals); i++ { //
		halfDiagonals[i] = make([]ExtendedBigInt, maxNCopy-i*2)
		centralBinomialCoefficient := big.NewInt(0)
		//centralBinomialCoefficient.Add(halfDiagonals[i-1][1].Value, halfDiagonals[i-1][1].Value)
		centralBinomialCoefficient.Set(halfDiagonals[i-1][1].Value).Lsh(centralBinomialCoefficient, 1)
		halfDiagonals[i][0] = ExtendedBigInt{centralBinomialCoefficient, uint16(centralBinomialCoefficient.BitLen())}
		for j := 1; j < len(halfDiagonals[i]); j++ {
			element := big.NewInt(0)
			element.Add(halfDiagonals[i][j-1].Value, halfDiagonals[i-1][j+1].Value)
			halfDiagonals[i][j] = ExtendedBigInt{element, uint16(element.BitLen())}
		}
		bar.Add(1)
	}

	// Estimate cache size
	if estimateCacheSize {
		numCacheBytes := uint64(0)
		for i := 0; i < len(halfDiagonals); i++ {
			for j := 1; j < len(halfDiagonals[i]); j++ {

				numWords := halfDiagonals[i][j].BitLength >> 6
				remainder := halfDiagonals[i][j].BitLength & 63
				numCacheBytes += uint64(numWords) << 3
				if remainder > 0 {
					numCacheBytes += 8
				}

				numCacheBytes += 2 // length (16bits)
			}
		}
		if !Quiet {
			fmt.Printf("Cache size (GB): %.3f\n", float32(numCacheBytes)/(1<<30))
		}
	}

	elapsedTime := time.Since(startTime)
	if !Quiet {
		fmt.Printf("(N,k) generation time: %.3fs\n", elapsedTime.Seconds())
	}
}

// BinCoef returns the binomial coefficient C(n, k) as an ExtendedBigInt from the
// cache built by InitCache. It returns 0 when k > n and 1 for the k == 0 and
// n == k edges, and uses the symmetry C(n, k) = C(n, n-k) to read from the
// smaller half-diagonal. The cache must have been sized (via InitCache) to cover
// n; callers are responsible for ensuring this.
func BinCoef(n int, k int) ExtendedBigInt {
	//LargestN = max(LargestN, uint64(n))
	//LargestK = max(LargestK, uint64(k))

	if k > n {
		return zero
	}
	if k == 0 || n == k {
		return one
	}

	if k > (n >> 1) {
		//Miss++
		return halfDiagonals[n-k-1][(k<<1)-n] // col: 2k-n
	} // k <= n/2
	//Hit++
	return halfDiagonals[k-1][n-(k<<1)] // col: n-2k // good for decompressor!!

	// TODO write code to grow cache if needed
}
