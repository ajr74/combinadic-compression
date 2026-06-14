#!/usr/bin/env bash
#
# Per-file compressed-fraction breakdown for a corpus directory (companion to
# enwik9_test.sh). Reports the compressed fraction of each file INDEPENDENTLY at a
# fixed block size, plus the corpus total. Intended for the Silesia per-file table.
#
# RATIO ONLY -- throughput is deliberately not measured here, because each separate
# invocation rebuilds the one-time binomial-coefficient cache (InitCache), which
# would dominate the timing on the small files and make MB/s meaningless. Take
# throughput from the concatenated run instead:
#     cat $(ls -1 data/silesia/* | sort) > data/silesia.bin
#     INPUT=data/silesia.bin KANZI=/path/to/Kanzi experiments/enwik9_test.sh
#
# Usage:
#   DIR=data/silesia BLOCK=1024 experiments/silesia_perfile.sh
#   DIR=data/silesia BLOCK=1024 KANZI=/path/to/Kanzi experiments/silesia_perfile.sh
# Environment overrides:
#   DIR      directory of corpus files   (default data/silesia)
#   BLOCK   block size                 (default 1024)
#   JOBS     concurrent jobs             (default 8)
#   KANZI    Kanzi binary (optional)     (adds a matched-block Huffman column;
#                                          Kanzi's minimum block is 1024)
#
# Requires a Go toolchain and awk. Each file is checked to round-trip (XXH3). Files
# are copied into a temp dir before compressing, so the corpus directory is never
# touched.
set -euo pipefail

DIR=${DIR:-data/silesia}
BLOCK=${BLOCK:-1024}
JOBS=${JOBS:-8}
KANZI=${KANZI:-}

# Locate the module root (the directory containing go.mod) by walking up from this
# script, so it works wherever it sits under the repo (experiments/, experiments/scripts/, ...).
ROOT=$(cd "$(dirname "$0")" && pwd)
while [ "$ROOT" != "/" ] && [ ! -f "$ROOT/go.mod" ]; do ROOT=$(dirname "$ROOT"); done
[ -f "$ROOT/go.mod" ] || { echo "could not find go.mod above $(dirname "$0")" >&2; exit 1; }
[ -d "$DIR" ] || { echo "directory not found: $DIR (set DIR=...)" >&2; exit 1; }
DIR="$(cd "$DIR" && pwd)" # absolute

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
go build -o "$TMP/ajz" "$ROOT"

frac() { awk -v n="$1" -v d="$2" 'BEGIN { printf "%.4f", n / d }'; }
fsize() { wc -c <"$1" | tr -d ' '; }

have_kanzi=0
[ -n "$KANZI" ] && [ -x "$KANZI" ] && have_kanzi=1

if [ "$have_kanzi" = 1 ]; then
	printf "%-16s %-12s %-10s %-10s\n" "file" "bytes" "this work" "kanzi Huf"
else
	printf "%-16s %-12s %-10s\n" "file" "bytes" "this work"
fi

tot_in=0
tot_out=0
tot_kanzi=0

for f in $(ls -1 "$DIR" | sort); do
	src="$DIR/$f"
	[ -f "$src" ] || continue
	in=$(fsize "$src")

	cp "$src" "$TMP/in"
	"$TMP/ajz" -k -b "$BLOCK" -j "$JOBS" "$TMP/in" >/dev/null 2>&1
	out=$(fsize "$TMP/in.ajz")

	# round-trip check (decompress a copy; never clobber the staged input)
	cp "$TMP/in.ajz" "$TMP/d.ajz"
	if ! "$TMP/ajz" -d -k -j "$JOBS" "$TMP/d.ajz" 2>&1 | tr '\r' '\n' | grep -aq 'hashes agree'; then
		echo "  WARNING: round-trip failed for $f at N=$BLOCK" >&2
	fi

	tot_in=$((tot_in + in))
	tot_out=$((tot_out + out))

	if [ "$have_kanzi" = 1 ]; then
		kb="$BLOCK"
		[ "$BLOCK" -lt 1024 ] && kb=1024 # Kanzi minimum block
		"$KANZI" -c -t None -b "$kb" -e Huffman -i "$src" -o "$TMP/e.knz" -j "$JOBS" -f >/dev/null 2>&1
		ko=$(fsize "$TMP/e.knz")
		tot_kanzi=$((tot_kanzi + ko))
		printf "%-16s %-12s %-10s %-10s\n" "$f" "$in" "$(frac "$out" "$in")" "$(frac "$ko" "$in")"
		rm -f "$TMP/e.knz"
	else
		printf "%-16s %-12s %-10s\n" "$f" "$in" "$(frac "$out" "$in")"
	fi

	rm -f "$TMP/in" "$TMP/in.ajz" "$TMP/d.ajz" "$TMP/d"
done

echo
if [ "$have_kanzi" = 1 ]; then
	printf "%-16s %-12s %-10s %-10s\n" "TOTAL" "$tot_in" "$(frac "$tot_out" "$tot_in")" "$(frac "$tot_kanzi" "$tot_in")"
else
	printf "%-16s %-12s %-10s\n" "TOTAL" "$tot_in" "$(frac "$tot_out" "$tot_in")"
fi
