# Combinadic Compression

A lossless byte-stream compressor based on **enumerative (combinadic) coding**.
A stream is split into fixed-length blocks; within each block the positions of
every byte value form disjoint bitsets, which are encoded by their
colexicographic ranks — so each block becomes a short sequence of integer fields
(the per-symbol counts and these ranks), each over a known finite range.

The scheme is an **order-0 entropy coder**: it reaches the order-0 entropy of its
input, matching (and, at a matched block size, bettering) classical order-0
coders such as Huffman, range, and ANS. It is *slower* than those streaming
coders because of its big-integer arithmetic, so its interest is structural
rather than raw speed — see [Status](#status) and the [paper](#paper).

> ⚠️ **Research prototype.** This is an experimental codec accompanying a paper,
> not a production compressor. In particular, like `gzip` it **deletes the input
> file by default** — pass `-k` to keep it.

## Build

```sh
go build -o ajz .
```

Requires Go 1.26+.

## Usage

```sh
# Compress foo.txt -> foo.txt.ajz  (original removed unless -k)
./ajz -k -b 1024 -j 8 foo.txt

# Decompress foo.txt.ajz -> foo.txt  (verifies an XXH3 integrity hash)
./ajz -d -k foo.txt.ajz

# Quiet compression: no progress bar or timings, errors still reported
./ajz -q -b 1024 foo.txt

# Encrypt while compressing (full mode); the key comes from a file you control
./ajz -enc full -keyfile mykey.bin foo.txt
# Decrypt + decompress (the mode is read from the header; just supply the key)
./ajz -d -keyfile mykey.bin foo.txt.ajz
```

| Flag          | Default  | Meaning                                              |
|---------------|----------|------------------------------------------------------|
| `-d`          | `false`  | Decompress (otherwise compress).                     |
| `-k`          | `false`  | Keep the input file (default removes it on success). |
| `-b N`        | `1024`   | Block size in bytes (compression only).              |
| `-j N`        | `NumCPU` | Number of concurrent worker jobs.                    |
| `-noref`      | `false`  | Per-record mode: each block encodes its own alphabet in full, with no stream-global reference alphabet (compression only). |
| `-q`          | `false`  | Quiet: suppress the progress bar and informational output (errors are still printed). |
| `-enc MODE`   | `""`     | Encrypt while compressing: `full` (every field) or `query` (queryable dial); empty disables. Requires `-keyfile`. |
| `-keyfile F`  | `""`     | Path to the master key file (raw bytes). Required to encrypt, and to decrypt an encrypted file. |
| `-cpuprofile` | `""`     | Write a CPU profile to the named file.               |
| `-memprofile` | `""`     | Write a heap profile to the named file.              |

Notes:

- Compressed files use the `.ajz` suffix and begin with magic bytes; decompression
  requires a `.ajz` name and re-checks a 64-bit **XXH3** hash of the original.
- The `-keyfile` holds raw key material — use **high-entropy random bytes** (32
  recommended): `head -c 32 /dev/urandom > mykey.bin`. Any non-empty file is accepted; a
  key that is not exactly 32 bytes is SHA-256-reduced to 32 bytes for the AES-256/ChaCha20
  backends.
- The block size is stored as a 16-bit field, so the format can *record* sizes
  up to `65535`, but the practical ceiling is much lower. The binomial-coefficient
  cache grows faster than quadratically with the block, so blocks in the tens of
  thousands are infeasible, and `InitCache` rejects sizes near the 16-bit maximum
  to avoid overflowing an internal bit-length field. Larger blocks compress
  slightly better but are markedly slower (the per-block ranking cost is
  super-linear in the block size).

## Status

- **Ratio:** order-0 optimal. On `enwik9`, at a matched block size it is the most
  compact of the order-0 coders; all converge to the source's order-0 entropy as
  the block grows.
- **Speed:** roughly 35–265 MB/s depending on block size, i.e. about an order of
  magnitude slower than table-driven streaming coders at the same ratio.
- **Best fit:** inputs over a *constrained* byte alphabet (e.g. nucleotide data
  `{A,C,G,T}` compresses to its 2-bits-per-symbol limit); a full-byte uniform
  source is incompressible.
- A **size-preserving compression+encryption** variant is prototyped in
  `util` (`EncryptRank`/`DecryptRank`): every field of a block is an integer over
  a known range, so each can be encrypted in place at no size cost. Which fields
  you encrypt is a leakage/queryability dial — from the position ranks alone
  (leaving the counts as a cleartext skip-table) to the whole block (leaking only
  its compressed length). It is wired into the CLI via `-enc`/`-keyfile`
  (above), which encrypts in place at no size cost. This is a **reference
  prototype**: the keystream is `SHA-256` in counter mode (not a vetted stream
  cipher) and there is no per-block authentication yet, so a wrong key fails the
  integrity check rather than being rejected up front — not hardened for
  production use.

## Repository layout

```
.                  CLI entry point (main.go)
compressor/        block encoder (Process)
decompressor/      block decoder (Process)
util/              binomial-coefficient cache, bit reader/writer,
                   bitset helpers, rank cipher prototype
integration/       black-box CLI round-trip tests (build the binary, exec it)
experiments/       reproducible benchmark scripts behind the paper's tables
doc/               the paper (article.tex and sections)
```

## Tests

```sh
# Unit, property, and integration tests
go test ./...

# Skip the slower CLI integration tests
go test -short ./...
```

## Experiments

Every table in the paper is reproduced by a script in `experiments/scripts/`,
with the saved outputs in `experiments/results/`:

| Paper table / result                               | Script(s) in `experiments/scripts/`                       |
|----------------------------------------------------|-----------------------------------------------------------|
| `enwik9` vs Kanzi at matched blocks; context codecs | `enwik9_test.sh`                                         |
| Silesia per-file; Canterbury                       | `silesia_perfile.sh`, `canterbury_perfile_test_runner.sh` |
| Stream vs per-record mode                          | `mode_compare.sh`                                         |
| Timestamp record store (real loghub Hadoop)        | `hadoop_ts_extract.sh` → `record_store_test.sh`           |
| Global-model order-0 baseline                      | `global_model_baseline.py` (needs numpy)                  |
| Alphabet-size sweep (fraction and throughput)      | `alphabet_test.sh` (with `alphabetgen/`)                  |
| Constrained-alphabet genomics                      | `fastq_extract.sh` → `genomics_test.sh`                   |
| Encryption overhead                                | `go test -bench=BenchmarkEncryption ./decompressor/`      |

```sh
# enwik9 vs Kanzi entropy codecs at matched block sizes
INPUT=data/enwik9 KANZI=/path/to/kanzi experiments/scripts/enwik9_test.sh

# alphabet-size sweep; MODE=time reports throughput instead of compressed fraction
KANZI=/path/to/kanzi experiments/scripts/alphabet_test.sh
MODE=time experiments/scripts/alphabet_test.sh
```

`KANZI` (path to a [Kanzi](https://github.com/flanglet/kanzi-go) binary) is
optional and only enables the comparison columns.

## Paper

A full description — algorithm, decompressor, implementation, experiments, and
applications — is in `doc/` (`article.tex`); build it with `pdflatex` + `bibtex`.

## License

Released under the [MIT License](LICENSE).

## Acknowledgements

Claude (Anthropic) was used as an AI assistant — for surveying related prior work,
for scripting the benchmark experiments, for the subsequent development and performance
tuning of the implementation, and for technical writing. The core combinadic
compression algorithm and its codec were conceived and developed independently and
solely by the author, predating this assistance; the author directed and verified all
AI-assisted work and is responsible for the result.
