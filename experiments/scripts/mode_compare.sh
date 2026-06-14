#!/usr/bin/env bash
#
# Stream vs per-record comparison for this codec (paper: Section "Two modes").
# For each block size, compresses INPUT in both modes and reports the compressed
# fraction of each and their difference --- the alphabet amortisation that stream
# mode buys over per-record (-noref) mode, measured directly.
#
# Ratio only: compressed fractions are deterministic, so no repeats are needed.
# Pass VERIFY=1 to additionally XXH3 round-trip every run (much slower).
#
# Usage:
#   INPUT=data/enwik9 experiments/mode_compare.sh
#   INPUT=data/silesia.bin BLOCKS="1024 4096" experiments/mode_compare.sh
# Environment overrides:
#   INPUT    benchmark file                 (default data/enwik9)
#   BLOCKS  space-separated block sizes    (default "512 1024 2048 4096 8192")
#   JOBS     concurrent jobs                 (default 8)
#   VERIFY   1 = also round-trip each run    (slow; default 0)
#
# Requires a Go toolchain and awk. The input file is staged into a temp dir, so the
# original is never touched and no .ajz files are left beside it.
#
# Runtime note: each compression of enwik9 is ~15 s, so the default 5x2 grid is a
# few minutes; VERIFY adds a ~70 s decompression per run.
set -euo pipefail

#INPUT=${INPUT:-data/enwik9}
BLOCKS=${BLOCKS:-512 1024 2048 4096 8192}
JOBS=${JOBS:-8}
VERIFY=${VERIFY:-0}

# Locate the module root (the directory containing go.mod) by walking up from this
# script, so it works wherever it sits under the repo (experiments/, experiments/scripts/, ...).
ROOT=$(cd "$(dirname "$0")" && pwd)
while [ "$ROOT" != "/" ] && [ ! -f "$ROOT/go.mod" ]; do ROOT=$(dirname "$ROOT"); done
[ -f "$ROOT/go.mod" ] || { echo "could not find go.mod above $(dirname "$0")" >&2; exit 1; }
[ -f "$INPUT" ] || { echo "input not found: $INPUT (set INPUT=...)" >&2; exit 1; }
INPUT="$(cd "$(dirname "$INPUT")" && pwd)/$(basename "$INPUT")" # absolute
SIZE=$(wc -c <"$INPUT" | tr -d ' ')

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
go build -o "$TMP/ajz" "$ROOT"
cp "$INPUT" "$TMP/in"

fsize() { wc -c <"$1" | tr -d ' '; }

# compress_size <flag> <block> : compress TMP/in in the given mode and echo the
# resulting .ajz size. <flag> is "" for stream mode or "-noref" for per-record.
compress_size() {
	local flag="$1" W="$2"
	"$TMP/ajz" -k $flag -b "$W" -j "$JOBS" "$TMP/in" >/dev/null 2>&1
	local o
	o=$(fsize "$TMP/in.ajz")
	if [ "$VERIFY" = 1 ]; then
		cp "$TMP/in.ajz" "$TMP/d.ajz"
		if ! "$TMP/ajz" -d -k -j "$JOBS" "$TMP/d.ajz" 2>&1 | tr '\r' '\n' | grep -aq 'hashes agree'; then
			echo "  WARNING: round-trip failed (mode=${flag:-stream}, N=$W)" >&2
		fi
		rm -f "$TMP/d" "$TMP/d.ajz"
	fi
	rm -f "$TMP/in.ajz"
	echo "$o"
}

echo "input: $INPUT ($SIZE bytes), jobs: $JOBS"
printf "%-8s %-10s %-12s %-10s %-8s\n" "N" "stream" "per-record" "delta" "delta%"
for W in $BLOCKS; do
	s=$(compress_size "" "$W")
	p=$(compress_size "-noref" "$W")
	awk -v W="$W" -v s="$s" -v p="$p" -v d="$SIZE" 'BEGIN {
		sf = s / d; pf = p / d; dl = pf - sf;
		printf "%-8s %-10.4f %-12.4f %+-10.4f %+.2f%%\n", W, sf, pf, dl, (sf > 0 ? 100 * dl / sf : 0)
	}'
done
