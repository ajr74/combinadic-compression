package main

import (
	"ajz/compressor"
	"ajz/decompressor"
	"ajz/util"
	"bufio"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"runtime/pprof"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bits-and-blooms/bitset"
	"github.com/schollz/progressbar/v3"
	"github.com/zeebo/xxh3"
)

func check(e error) {
	if e != nil {
		panic(e)
	}
}

// logf prints informational output unless -q (quiet) is set. Error messages do
// not use logf, so they are reported even in quiet mode.
func logf(format string, a ...any) {
	if !*quiet {
		fmt.Printf(format, a...)
	}
}

const suffix = ".ajz"

var cpuprofile = flag.String("cpuprofile", "", "write cpu profile to `file`")
var memprofile = flag.String("memprofile", "", "write memory profile to `file`")
var blockSize = flag.Int("b", 1024, "block size in bytes")
var decompress = flag.Bool("d", false, "decompress")
var keep = flag.Bool("k", false, "keep original file")
var jobs = flag.Int("j", runtime.NumCPU(), "number of jobs (threads)")
var noref = flag.Bool("noref", false, "per-record mode: each block encodes its own alphabet in full, with no stream-global reference alphabet")
var quiet = flag.Bool("q", false, "quiet: suppress the progress bar and informational output (errors are still printed)")
var keyfile = flag.String("keyfile", "", "path to the master key file (required with -enc)")
var encMode = flag.String("enc", "", "encrypt: \"full\" (every field) or \"query\" (queryable dial); empty disables encryption")

// File signature: a high-bit canary byte (detects 7-bit/text-mode corruption and
// marks the file as binary), the ASCII tag "AJZ" matching the .ajz extension, and
// a trailing CR-LF that catches line-ending translation.
var magicBytes = []byte{0x8A, 'A', 'J', 'Z', 0x0D, 0x0A}

// Format flags byte (written after the magic). Bit 0 marks an encrypted file;
// bits 1-2 hold the mode (1 = full, 2 = queryable).
const flagEncrypted byte = 1 << 0

// encFlagsByte builds the format flags byte for an encrypted file: the encrypted
// bit set, plus the mode in bits 1-2 (1 = full, 2 = queryable). See flagEncrypted.
func encFlagsByte(full bool) byte {
	if full {
		return flagEncrypted | (1 << 1) // mode 1 = full
	}
	return flagEncrypted | (2 << 1) // mode 2 = query
}

// parseEncFlags is the inverse of encFlagsByte: it reports whether the file is
// encrypted and, if so, whether the mode is full (otherwise queryable).
func parseEncFlags(f byte) (on, full bool) {
	on = f&flagEncrypted != 0
	full = ((f >> 1) & 0x3) == 1
	return
}

func main() {

	startTime := time.Now()
	flag.Parse()

	// In quiet mode the progress bar is routed to io.Discard so it produces no
	// output; otherwise it uses the library default (stderr). util.Quiet is set
	// so the binomial-cache build (util.InitCache) is silenced too.
	util.Quiet = *quiet
	barWriter := io.Writer(os.Stderr)
	if *quiet {
		barWriter = io.Discard
	}

	// Encryption configuration. The master key is read from -keyfile; the per-file
	// salt is generated at compression and stored in the header. -enc selects the
	// mode at compression; at decompression the mode is read from the header.
	if *encMode != "" && *encMode != "full" && *encMode != "query" {
		fmt.Printf("Error: -enc must be \"full\" or \"query\" (got %q)\n", *encMode)
		return
	}
	var masterKey []byte
	if *keyfile != "" {
		var keyErr error
		masterKey, keyErr = os.ReadFile(*keyfile)
		if keyErr != nil || len(masterKey) == 0 {
			fmt.Printf("Error reading key file %q: %v\n", *keyfile, keyErr)
			return
		}
	}
	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			log.Fatal("could not create CPU profile: ", err)
		}
		defer f.Close()
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Fatal("could not start CPU profile: ", err)
		}
		defer pprof.StopCPUProfile()
	}
	filename := flag.Arg(0)

	numJobs := uint16(*jobs)
	if numJobs < 1 {
		numJobs = 1
	}
	logf("Num jobs: %d\n", numJobs)

	buffer16 := make([]byte, 2)
	buffer32 := make([]byte, 4)
	buffer64 := make([]byte, 8)
	referenceAlphabetBitSet := bitset.New(256)

	if *decompress {

		if !strings.HasSuffix(filename, suffix) {
			fmt.Printf("Error. File must end with %s\n", suffix)
			return
		}
		readFile, readFileErr := os.Open(filename)
		check(readFileErr)

		magicIn := make([]byte, len(magicBytes))
		numMagicBytesRead, magicBytesErr := readFile.Read(magicIn)
		if magicBytesErr != nil || numMagicBytesRead != len(magicIn) {
			fmt.Printf("Error reading magic bytes: %v\n", magicBytesErr)
			return
		}

		if !slices.Equal(magicBytes, magicIn) {
			fmt.Printf("Error: magic bytes don't agree!\n")
			return
		}

		flagsIn := make([]byte, 1)
		if _, err := io.ReadFull(readFile, flagsIn); err != nil {
			fmt.Printf("Error reading format flags: %v\n", err)
			return
		}
		encOn, encFull := parseEncFlags(flagsIn[0])
		var subkey []byte
		if encOn {
			if masterKey == nil {
				fmt.Printf("Error: file is encrypted; supply the key with -keyfile\n")
				return
			}
			salt := make([]byte, util.FileSaltLen)
			if _, err := io.ReadFull(readFile, salt); err != nil {
				fmt.Printf("Error reading salt: %v\n", err)
				return
			}
			subkey = util.DeriveFileKey(masterKey, salt)
		}

		if _, err := io.ReadFull(readFile, buffer16); err != nil {
			fmt.Printf("Error reading block size: %v\n", err)
			return
		}
		bufferSize := int(binary.BigEndian.Uint16(buffer16))
		util.InitCache(max(bufferSize, 256) + 10) // alphabet ranking needs C(256,.), even for small blocks
		decompressionTime := time.Now()
		if _, err := io.ReadFull(readFile, buffer32); err != nil {
			fmt.Printf("Error reading block count: %v\n", err)
			return
		}
		numBlocks := binary.BigEndian.Uint32(buffer32)

		if _, err := io.ReadFull(readFile, buffer16); err != nil {
			fmt.Printf("Error reading remainder: %v\n", err)
			return
		}
		remainder := binary.BigEndian.Uint16(buffer16)

		referenceAlphabetBytes := make([]byte, 32)
		if _, err := io.ReadFull(readFile, referenceAlphabetBytes); err != nil {
			fmt.Printf("Error reading reference alphabet: %v\n", err)
			return
		}
		if encOn {
			util.DecryptBytesField(referenceAlphabetBytes, subkey, util.ReferenceAlphabetNonce())
		}
		referenceAlphabetBitSetWords := make([]uint64, 4)
		for index := range referenceAlphabetBitSetWords {
			referenceAlphabetBitSetWords[index] = binary.BigEndian.Uint64(referenceAlphabetBytes[index*8:])
		}
		referenceAlphabetBitSet.SetBitsetFrom(referenceAlphabetBitSetWords)

		fname := strings.TrimSuffix(filename, suffix)

		writeFile, _ := os.Create(fname)
		writer := bufio.NewWriterSize(writeFile, bufferSize*4096*2)

		fileInfo, _ := os.Stat(filename)
		decompressDesc := "Decompressing:"
		if encOn {
			decompressDesc = "Decrypting + decompressing:"
		}
		bar := progressbar.NewOptions64(fileInfo.Size(),
			progressbar.OptionSetWriter(barWriter),
			progressbar.OptionShowCount(),
			progressbar.OptionSetDescription(decompressDesc),
			progressbar.OptionShowBytes(false),
			progressbar.OptionThrottle(10*time.Millisecond),
			progressbar.OptionClearOnFinish(),
			progressbar.OptionSetPredictTime(true))

		_ = bar.Add(len(magicBytes) + 8)
		reader := bufio.NewReaderSize(readFile, bufferSize*4096*2)
		hasher := xxh3.New()

		// Worker pool with block batching (see the compress path for rationale):
		// each job/result carries a batch of blocks so the channel/goroutine
		// handoff cost is amortized over many blocks. A single collector writes
		// and hashes the decompressed output in block order; batches cycle
		// through a free list whose contiguous output buffer is recycled once the
		// batch is written. batchSize scales inversely with block size.
		numWorkers := int(numJobs)
		batchSize := 262144 / bufferSize
		if batchSize < 1 {
			batchSize = 1
		}
		batchesInFlight := numWorkers * 2

		type decompressBatch struct {
			firstBlock uint32
			n          int
			inputs     [][]byte // n compressed inputs (allocated per block)
			nwBytes    []uint   // n block lengths
			outBack    []byte   // batchSize*bufferSize contiguous output storage
			outs       [][]byte // n output slices (into outBack)
		}

		freeBatches := make(chan *decompressBatch, batchesInFlight)
		for b := 0; b < batchesInFlight; b++ {
			freeBatches <- &decompressBatch{
				inputs:  make([][]byte, batchSize),
				nwBytes: make([]uint, batchSize),
				outBack: make([]byte, batchSize*bufferSize),
				outs:    make([][]byte, batchSize),
			}
		}
		jobs := make(chan *decompressBatch, batchesInFlight)
		results := make(chan *decompressBatch, batchesInFlight)

		var workerWg sync.WaitGroup
		for w := 0; w < numWorkers; w++ {
			workerWg.Add(1)
			go func() {
				defer workerWg.Done()
				for batch := range jobs {
					for j := 0; j < batch.n; j++ {
						out := batch.outBack[j*bufferSize : j*bufferSize+int(batch.nwBytes[j])]
						if encOn {
							// A wrong key can yield invalid intermediate state; recover so it
							// fails via the XXH3 check rather than crashing a worker.
							func() {
								defer func() { _ = recover() }()
								decompressor.ProcessEncrypted(batch.inputs[j], batch.nwBytes[j], referenceAlphabetBitSet, out,
									util.Cipher{Subkey: subkey, BlockIndex: batch.firstBlock + uint32(j), Full: encFull})
							}()
						} else {
							decompressor.Process(batch.inputs[j], batch.nwBytes[j], referenceAlphabetBitSet, out)
						}
						batch.outs[j] = out
						batch.inputs[j] = nil // release compressed input
					}
					results <- batch
				}
			}()
		}
		go func() {
			workerWg.Wait()
			close(results)
		}()

		// Collector: reorder batches by first block number, write + hash each
		// batch's blocks in order, recycling the batch once written.
		collectorDone := make(chan struct{})
		go func() {
			defer close(collectorDone)
			pending := make(map[uint32]*decompressBatch)
			next := uint32(0)
			for batch := range results {
				pending[batch.firstBlock] = batch
				for {
					b, ok := pending[next]
					if !ok {
						break
					}
					delete(pending, next)
					for j := 0; j < b.n; j++ {
						if numBytesWritten, err := writer.Write(b.outs[j]); err != nil || numBytesWritten != len(b.outs[j]) {
							fmt.Printf("Error writing uncompressed block (%d/%d): %s\n", numBytesWritten, len(b.outs[j]), err)
						}
						hasher.Write(b.outs[j])
						b.outs[j] = nil
					}
					next += uint32(b.n)
					freeBatches <- b // recycle now that the batch is written
				}
			}
		}()

		// Reader: read compressed blocks sequentially into batches and dispatch.
		readFailed := false
		for i := uint32(0); i < numBlocks; {
			batch := <-freeBatches
			batch.firstBlock = i
			batch.n = 0
			for j := 0; j < batchSize && i < numBlocks; j++ {
				numBytesRead1, err := io.ReadFull(reader, buffer16)
				_ = bar.Add(numBytesRead1)
				if err != nil || numBytesRead1 != len(buffer16) {
					fmt.Printf("Error reading compressed block length (%d/%d): %s\n", numBytesRead1, len(buffer16), err)
					readFailed = true
					break
				}
				compressedBytes := binary.BigEndian.Uint16(buffer16)
				readBuffer := make([]byte, compressedBytes)
				numBytesRead2, err := io.ReadFull(reader, readBuffer)
				_ = bar.Add(numBytesRead2)
				if err != nil || numBytesRead2 != int(compressedBytes) {
					fmt.Printf("Error reading compressed block data (%d/%d): %s\n", numBytesRead2, compressedBytes, err)
					readFailed = true
					break
				}
				nwb := uint(bufferSize)
				if i == numBlocks-1 && remainder > 0 {
					nwb = uint(remainder)
				}
				batch.inputs[j] = readBuffer
				batch.nwBytes[j] = nwb
				batch.n++
				i++
			}
			jobs <- batch
			if readFailed {
				break
			}
		}
		close(jobs)
		<-collectorDone
		if readFailed {
			return
		}

		flushErr := writer.Flush()
		if flushErr != nil {
			fmt.Printf("Problem flushing writer: %s\n", flushErr)
			return
		}

		numBytesRead, err2 := io.ReadFull(reader, buffer64)
		_ = bar.Add(numBytesRead)
		if err2 != nil || numBytesRead != len(buffer64) {
			fmt.Printf("Error reading XXH3 hash: %s\n", err2)
		}
		if encOn {
			util.DecryptBytesField(buffer64, subkey, util.HashNonce())
		}
		_ = bar.Finish()

		if hasher.Sum64() == binary.BigEndian.Uint64(buffer64) {
			logf("\033[32m" + "S U C C E S S :: XXH3 hashes agree!\n" + "\033[0m") // print in green
			if !*keep {
				os.Remove(filename)
			}
		} else {
			fmt.Printf("\033[31m" + "F A I L U R E :: XXH3 hashes disagree!\n" + "\033[0m") // print in red
		}
		decompTimeLabel := "Decompression time"
		if encOn {
			if encFull {
				decompTimeLabel = "Full decryption + decompression time"
			} else {
				decompTimeLabel = "Queryable decryption + decompression time"
			}
		}
		logf("%s: %.3fs\n", decompTimeLabel, time.Since(decompressionTime).Seconds())
	} else {
		bufferSize := *blockSize
		util.InitCache(max(bufferSize, 256) + 10) // alphabet ranking needs C(256,.), even for small blocks
		compressionTime := time.Now()
		fileInfo, _ := os.Stat(filename)
		compressDesc := "Compressing:"
		if *encMode != "" {
			compressDesc = "Compressing + encrypting:"
		}
		bar := progressbar.NewOptions64(fileInfo.Size(),
			progressbar.OptionSetWriter(barWriter),
			progressbar.OptionShowCount(),
			progressbar.OptionSetDescription(compressDesc),
			progressbar.OptionShowBytes(false),
			progressbar.OptionThrottle(10*time.Millisecond),
			progressbar.OptionClearOnFinish(),
			progressbar.OptionSetPredictTime(true))
		readFile, readFileErr := os.Open(filename)
		check(readFileErr)
		defer readFile.Close()
		writeFile, writeFileErr := os.Create(filename + suffix)
		check(writeFileErr)
		defer writeFile.Close()
		reader := bufio.NewReaderSize(readFile, bufferSize*4096*2)
		writer := bufio.NewWriterSize(writeFile, bufferSize*4096*2)

		hasher := xxh3.New()

		numBytesWritten0, err0 := writer.Write(magicBytes)
		if err0 != nil || numBytesWritten0 != len(magicBytes) {
			fmt.Printf("Error writing magic bytes: %s\n", err0)
			return
		}

		wantEncrypt := *encMode != ""
		wantFull := *encMode == "full"
		if wantEncrypt && masterKey == nil {
			fmt.Printf("Error: -enc requires -keyfile\n")
			return
		}
		var salt, subkey []byte
		if wantEncrypt {
			var saltErr error
			salt, saltErr = util.NewFileSalt()
			if saltErr != nil {
				fmt.Printf("Error generating salt: %v\n", saltErr)
				return
			}
			subkey = util.DeriveFileKey(masterKey, salt)
		}
		flagsByte := byte(0)
		if wantEncrypt {
			flagsByte = encFlagsByte(wantFull)
		}
		if _, err := writer.Write([]byte{flagsByte}); err != nil {
			fmt.Printf("Error writing format flags: %v\n", err)
			return
		}
		if wantEncrypt {
			if _, err := writer.Write(salt); err != nil {
				fmt.Printf("Error writing salt: %v\n", err)
				return
			}
		}

		binary.BigEndian.PutUint16(buffer16, uint16(bufferSize))
		numBytesWritten1, err1 := writer.Write(buffer16)
		if err1 != nil || numBytesWritten1 != len(buffer16) {
			fmt.Printf("Error writing buffer size: %s\n", err1)
			return
		}

		numBlocks := uint32(fileInfo.Size() / int64(bufferSize))
		remainder := uint16(fileInfo.Size() % int64(bufferSize))
		if (remainder) != 0 {
			numBlocks++
		}

		binary.BigEndian.PutUint32(buffer32, numBlocks)
		numBytesWritten2, err2 := writer.Write(buffer32)
		if err2 != nil || numBytesWritten2 != len(buffer32) {
			fmt.Printf("Error writing number of blocks: %s\n", err2)
			return
		}

		binary.BigEndian.PutUint16(buffer16, remainder)
		numBytesWritten3, err3 := writer.Write(buffer16)
		if err3 != nil || numBytesWritten3 != len(buffer16) {
			fmt.Printf("Error writing number of remainder bytes: %s\n", err3)
			return
		}

		// In per-record mode (-noref) the reference alphabet is left empty, so each
		// block encodes its own alphabet in full (the symmetric difference of a
		// block's alphabet against the empty set is itself). The empty alphabet is
		// still written to the header below as zero words, so the decompressor needs
		// no special case --- it reconstructs an empty reference and the same
		// symmetric-difference step recovers each block's alphabet unchanged.
		if !*noref {
			peekBytes, peekError := reader.Peek(bufferSize * 3)
			if peekError == nil {
				for _, byteVal := range peekBytes {
					referenceAlphabetBitSet.Set(uint(byteVal))
				}
			} else {
				fmt.Printf("Peek error: %s\n", peekError)
			}
		}
		mode := "stream"
		if *noref {
			mode = "per-record"
		}
		logf("Reference alphabet bytes: %d (%s mode)\n", referenceAlphabetBitSet.Count(), mode)
		referenceAlphabetBytes := make([]byte, 32)
		for idx, word := range referenceAlphabetBitSet.Words() {
			binary.BigEndian.PutUint64(referenceAlphabetBytes[idx*8:], word)
		}

		if wantEncrypt {
			util.EncryptBytesField(referenceAlphabetBytes, subkey, util.ReferenceAlphabetNonce())
		}
		if referenceBytesWritten, referenceBytesWriteErr := writer.Write(referenceAlphabetBytes); referenceBytesWriteErr != nil || referenceBytesWritten != len(referenceAlphabetBytes) {
			fmt.Printf("Error writing reference alphabet: %s\n", referenceBytesWriteErr)
		}

		// Worker pool with block batching: each job/result carries a batch of
		// blocks, amortizing the channel/goroutine handoff cost over many
		// blocks (per-block coordination otherwise dominates for small
		// blocks). A single collector writes batches back in block order;
		// batches cycle through a free list that bounds blocks in flight.
		// batchSize scales inversely with block size, keeping in-flight memory
		// roughly constant and disabling batching once blocks are large enough.
		numWorkers := int(numJobs)
		batchSize := 262144 / bufferSize
		if batchSize < 1 {
			batchSize = 1
		}
		batchesInFlight := numWorkers * 2

		type compressBatch struct {
			firstBlock uint32
			n          int
			backing    []byte   // batchSize*bufferSize contiguous input storage
			inputs     [][]byte // n block input slices (into backing)
			datas      [][]byte // n compressed outputs
		}

		freeBatches := make(chan *compressBatch, batchesInFlight)
		for b := 0; b < batchesInFlight; b++ {
			freeBatches <- &compressBatch{
				backing: make([]byte, batchSize*bufferSize),
				inputs:  make([][]byte, batchSize),
				datas:   make([][]byte, batchSize),
			}
		}
		jobs := make(chan *compressBatch, batchesInFlight)
		results := make(chan *compressBatch, batchesInFlight)

		var workerWg sync.WaitGroup
		for w := 0; w < numWorkers; w++ {
			workerWg.Add(1)
			go func() {
				defer workerWg.Done()
				for batch := range jobs {
					for j := 0; j < batch.n; j++ {
						if wantEncrypt {
							batch.datas[j] = compressor.ProcessEncrypted(batch.inputs[j], referenceAlphabetBitSet,
								util.Cipher{Subkey: subkey, BlockIndex: batch.firstBlock + uint32(j), Full: wantFull})
						} else {
							batch.datas[j] = compressor.Process(batch.inputs[j], referenceAlphabetBitSet)
						}
					}
					results <- batch
				}
			}()
		}
		go func() {
			workerWg.Wait()
			close(results)
		}()

		// Collector: reorder batches by first block number and write each
		// batch's blocks in order, recycling the batch once written.
		collectorDone := make(chan struct{})
		go func() {
			defer close(collectorDone)
			buf16 := make([]byte, 2)
			pending := make(map[uint32]*compressBatch)
			next := uint32(0)
			for batch := range results {
				pending[batch.firstBlock] = batch
				for {
					b, ok := pending[next]
					if !ok {
						break
					}
					delete(pending, next)
					for j := 0; j < b.n; j++ {
						binary.BigEndian.PutUint16(buf16, uint16(len(b.datas[j])))
						if _, err := writer.Write(buf16); err != nil {
							fmt.Printf("Error writing compressed block length [%d]: %s\n", next+uint32(j), err)
						}
						if bytesWritten6, err6 := writer.Write(b.datas[j]); err6 != nil || bytesWritten6 != len(b.datas[j]) || bytesWritten6 == 0 {
							fmt.Printf("Error writing compressed block [%d]: %s\n", next+uint32(j), err6)
						}
						b.datas[j] = nil
					}
					next += uint32(b.n)
					freeBatches <- b // recycle now that the batch is written
				}
			}
		}()

		// Reader: fill batches with blocks read sequentially, hashing original
		// bytes in order, and dispatch each batch. Runs in this (main) goroutine.
		readFailed := false
		for i := uint32(0); i < numBlocks; {
			batch := <-freeBatches
			batch.firstBlock = i
			batch.n = 0
			for j := 0; j < batchSize && i < numBlocks; j++ {
				buf := batch.backing[j*bufferSize : (j+1)*bufferSize]
				numBytesRead4, err4 := reader.Read(buf)
				if err4 != nil {
					fmt.Printf("Error reading uncompressed block [%d]: %s\n", i, err4)
					readFailed = true
					break
				}
				bar.Add(numBytesRead4)
				hasher.Write(buf[:numBytesRead4])
				batch.inputs[j] = buf[:numBytesRead4]
				batch.n++
				i++
			}
			jobs <- batch
			if readFailed {
				break
			}
		}
		close(jobs)
		<-collectorDone
		if readFailed {
			return
		}
		bar.Finish()
		bar.Clear()

		binary.BigEndian.PutUint64(buffer64, hasher.Sum64())
		if wantEncrypt {
			util.EncryptBytesField(buffer64, subkey, util.HashNonce())
		}
		bytesWritten7, err7 := writer.Write(buffer64)
		if err7 != nil || bytesWritten7 != len(buffer64) {
			fmt.Printf("Error writing XXH3 hash: %s\n", err7)
			return
		}

		flushErr := writer.Flush()
		if flushErr != nil {
			fmt.Printf("Problem flushing writer: %s\n", flushErr)
		}

		if !*keep {
			os.Remove(filename)
		}
		compTimeLabel := "Compression time"
		if *encMode == "full" {
			compTimeLabel = "Compression + full encryption time"
		} else if *encMode == "query" {
			compTimeLabel = "Compression + queryable encryption time"
		}
		logf("%s: %.3fs\n", compTimeLabel, time.Since(compressionTime).Seconds())
	}

	logf("Total time: %.3fs\n", time.Since(startTime).Seconds())

	if *memprofile != "" {
		f, err := os.Create(*memprofile)
		if err != nil {
			log.Fatal("could not create memory profile: ", err)
		}
		defer f.Close()
		runtime.GC() // get up-to-date statistics
		if err := pprof.WriteHeapProfile(f); err != nil {
			log.Fatal("could not write memory profile: ", err)
		}
	}
}
