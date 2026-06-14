#!/usr/bin/env bash
#
# Extract the nucleotide base stream from a FASTQ file for the genomics experiment
# (Experiment B). FASTQ stores four lines per read -- header, sequence, '+',
# quality -- so the bases are line 2 of every 4-line record. This pulls those out,
# giving an {A,C,G,T} (+ rare N) byte stream: the order-0 input the codec's
# constrained-alphabet claim is about. Quality scores and headers are deliberately
# excluded; they have different distributions and are not part of the order-0
# base-compression claim.
#
# Usage:
#   experiments/fastq_extract.sh reads.fastq[.gz] [out.seq]
# Options via env:
#   NEWLINES=1   keep one read per line (default: strip newlines -> one pure stream)
#
# It also prints a symbol histogram and the order-0 entropy of the base stream to
# stderr -- the entropy/8 is the ideal compressed fraction (the floor the codec
# should approach), so you can read off the target before running the codec.
#
# Then compress at a read-length block and compare, e.g.:
#   ./ajz -k -b 150 data/ecoli.seq        # achieved fraction = .ajz size / .seq size
#   gzip -9 -c data/ecoli.seq | wc -c          # gzip baseline
#   # 2-bit-pack lower bound for pure ACGT = 0.25
set -euo pipefail

IN=${1:?usage: fastq_extract.sh reads.fastq[.gz] [out.seq]}
OUT=${2:-}
NEWLINES=${NEWLINES:-0}

[ -f "$IN" ] || { echo "input not found: $IN" >&2; exit 1; }

# Default output name: strip .gz then the .fastq/.fq extension, append .seq
if [ -z "$OUT" ]; then
	base="$IN"
	case "$base" in *.gz) base="${base%.gz}" ;; esac
	base="${base%.*}"
	OUT="${base}.seq"
fi

TMP=$(mktemp)
trap 'rm -f "$TMP"' EXIT

# Decompress if needed, then keep the sequence line of every 4-line record.
case "$IN" in
	*.gz) gzip -dc -- "$IN" ;;
	*) cat -- "$IN" ;;
esac | awk 'NR % 4 == 2' >"$TMP"

# Produce the output stream (newlines stripped by default for a pure base stream).
if [ "$NEWLINES" = 1 ]; then
	cp "$TMP" "$OUT"
else
	tr -d '\n' <"$TMP" >"$OUT"
fi

# Summary + order-0 entropy, computed over the per-read lines (awk-safe line lengths).
awk '
{
	reads++
	n = length($0)
	bases += n
	for (i = 1; i <= n; i++) h[substr($0, i, 1)]++
}
END {
	printf "reads:  %d\n", reads          > "/dev/stderr"
	printf "bases:  %d\n", bases          > "/dev/stderr"
	printf "symbol : count : fraction\n"  > "/dev/stderr"
	H = 0
	for (s in h) {
		p = h[s] / bases
		printf "  %s : %d : %.4f\n", s, h[s], p > "/dev/stderr"
		H -= p * log(p) / log(2)
	}
	printf "order-0 entropy: %.4f bits/base  (ideal compressed fraction of 8-bit bytes: %.4f)\n", H, H / 8 > "/dev/stderr"
}' "$TMP"

echo "wrote $OUT ($(wc -c <"$OUT" | tr -d ' ') bytes)" >&2
