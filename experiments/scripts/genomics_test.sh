#!/usr/bin/env bash
#
# Genomics constrained-alphabet experiment (paper: "Real constrained-alphabet data").
# Compresses extracted nucleotide base streams ({A,C,G,T}) with the codec at a matched
# block size and compares against gzip -9 and the 2-bit-per-symbol pack (fraction 0.25),
# the order-0 reference for a pure 4-symbol source. The codec runs on a COPY, so the
# input .seq files are never modified or deleted.
#
# Inputs are .seq base streams produced by fastq_extract.sh from FASTQ(.gz) reads.
# Reproduces the paper's N=1024 fractions:
#   SRR2627175_1.seq (E. coli)        ~0.2569
#   ERR006177_1.seq  (P. falciparum)  ~0.2436
#
# Usage:
#   experiments/scripts/genomics_test.sh [file1.seq file2.seq ...] \
#       > experiments/results/genomics_fastq_results.txt
#   # default inputs: data/SRR2627175_1.seq data/ERR006177_1.seq
# Environment overrides:
#   BLOCK   codec block size N   (default 1024, matching the paper)
#   JOBS    concurrent jobs      (default 8)
#   GZIP    1 = run the gzip -9 baseline, 0 = skip it (slow on large streams) (default 1)
set -euo pipefail

BLOCK=${BLOCK:-1024}
JOBS=${JOBS:-8}
GZIP=${GZIP:-1}

# Locate the module root (the directory containing go.mod) by walking up.
ROOT=$(cd "$(dirname "$0")" && pwd)
while [ "$ROOT" != "/" ] && [ ! -f "$ROOT/go.mod" ]; do ROOT=$(dirname "$ROOT"); done
[ -f "$ROOT/go.mod" ] || { echo "could not find go.mod above $(dirname "$0")" >&2; exit 1; }

if [ "$#" -gt 0 ]; then
	FILES=("$@")
else
	FILES=("$ROOT/data/SRR2627175_1.seq" "$ROOT/data/ERR006177_1.seq")
fi

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
go build -o "$TMP/ajz" "$ROOT"

echo "genomics constrained-alphabet test  (codec block N=$BLOCK, jobs=$JOBS)"
echo "fraction = compressed_bytes / original_bytes;  2-bit pack = 0.2500 reference floor"
echo
printf '%-22s %14s %14s %8s %14s %8s\n' "file" "orig" "codec" "c.frac" "gzip-9" "g.frac"
for f in "${FILES[@]}"; do
	if [ ! -f "$f" ]; then echo "missing: $f (run fastq_extract.sh first)" >&2; continue; fi
	orig=$(wc -c < "$f")
	cp "$f" "$TMP/in.seq"
	"$TMP/ajz" -q -k -b "$BLOCK" -j "$JOBS" "$TMP/in.seq"
	codec=$(wc -c < "$TMP/in.seq.ajz")
	cfrac=$(awk "BEGIN{printf \"%.4f\", $codec/$orig}")
	if [ "$GZIP" = "1" ]; then
		gz=$(gzip -9 -c "$f" | wc -c)
		gfrac=$(awk "BEGIN{printf \"%.4f\", $gz/$orig}")
	else
		gz="(skipped)"; gfrac="-"
	fi
	printf '%-22s %14s %14s %8s %14s %8s\n' "$(basename "$f")" "$orig" "$codec" "$cfrac" "$gz" "$gfrac"
	rm -f "$TMP/in.seq" "$TMP/in.seq.ajz"
done
