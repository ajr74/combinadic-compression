#!/usr/bin/env bash
#
# Reproduces Table 1 (tab:enwik9) of doc/experiments.tex: compress and
# decompress a benchmark file with this codec at several block sizes, and with
# Kanzi's entropy codecs (transforms disabled), reporting the compressed fraction
# and wall-clock compression/decompression throughput. The paper uses enwik9
# (the first 10^9 bytes of a Wikipedia dump) with eight jobs.
#
# Usage:
#   INPUT=data/enwik9 KANZI=/path/to/Kanzi experiments/enwik9_test.sh
# Environment overrides:
#   INPUT    path to the benchmark file        (default data/enwik9)
#   KANZI    path to a Kanzi binary            (Kanzi rows shown if set & executable)
#   JOBS     concurrent jobs                   (default 8)
#
# Requires a Go toolchain, awk, and /usr/bin/time. The codec is built with the
# go-bitarray linkname flag. Each this-work row is also checked to round-trip
# (XXH3) losslessly.
set -euo pipefail

INPUT=${INPUT:-data/enwik9}

THIS_WORK_BLOCKS=("512" "1024" "2048" "4096" "8192")

# Kanzi: order-0 codecs
ORDER_0_BLOCKS=("1024" "2048" "4096" "8192" "auto") # 1024 us the minimum Kanzi accepts
ORDER_0_CODECS=("None" "Huffman" "ANS0" "Range")

# Kanzi: other codecs
OTHER_BLOCKS=("auto")
OTHER_CODECS=("ANS1" "FPAQ" "TPAQ" "TPAQX" "CM")


KANZI=${KANZI:-}
JOBS=${JOBS:-8}

# Locate the module root (the directory containing go.mod) by walking up from this
# script, so it works wherever it sits under the repo (experiments/, experiments/scripts/, ...).
ROOT=$(cd "$(dirname "$0")" && pwd)
while [ "$ROOT" != "/" ] && [ ! -f "$ROOT/go.mod" ]; do ROOT=$(dirname "$ROOT"); done
[ -f "$ROOT/go.mod" ] || { echo "could not find go.mod above $(dirname "$0")" >&2; exit 1; }
[ -f "$INPUT" ] || {
	echo "input not found: $INPUT (set INPUT=...)" >&2
	exit 1
}
INPUT="$(cd "$(dirname "$INPUT")" && pwd)/$(basename "$INPUT")" # absolute
SIZE=$(wc -c <"$INPUT" | tr -d ' ')

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
go build -o "$TMP/ajz" "$ROOT"

realtime() { /usr/bin/time -p "$@" 2>"$TMP/t" >/dev/null; awk '/^real/ { print $2 }' "$TMP/t"; }
mbps() { awk -v s="$SIZE" -v t="$1" 'BEGIN { printf "%.0f", (s / 1048576) / t }'; }
frac() { awk -v n="$1" -v d="$SIZE" 'BEGIN { printf "%.4f", n / d }'; }
fsize() { wc -c <"$1" | tr -d ' '; }

printf "%-26s %-10s %-12s %-12s\n" "method" "fraction" "comp MB/s" "decomp MB/s"

# This codec at each block size (one staged copy reused; -k keeps the input).
cp "$INPUT" "$TMP/in"
for W in "${THIS_WORK_BLOCKS[@]}"; do
	ct=$(realtime "$TMP/ajz" -k -b "$W" -j "$JOBS" "$TMP/in")
	o=$(fsize "$TMP/in.ajz")
	cp "$TMP/in.ajz" "$TMP/d.ajz" # decompress a copy so the input is never clobbered
	if ! "$TMP/ajz" -d -k -j "$JOBS" "$TMP/d.ajz" 2>&1 | tr '\r' '\n' | grep -aq 'hashes agree'; then
		echo "  WARNING: round-trip check failed at N=$W" >&2
	fi
	dt=$(realtime "$TMP/ajz" -d -k -j "$JOBS" "$TMP/d.ajz")
	printf "%-26s %-10s %-12s %-12s\n" "this work N=$W" "$(frac "$o")" "$(mbps "$ct")" "$(mbps "$dt")"
	rm -f "$TMP/in.ajz" "$TMP/d" "$TMP/d.ajz"
done
rm -f "$TMP/in"

# Kanzi: order-0 entropy codecs with the transform disabled.
if [ -n "$KANZI" ] && [ -x "$KANZI" ]; then
	for E in "${ORDER_0_CODECS[@]}"; do
	  for B in "${ORDER_0_BLOCKS[@]}"; do
      ct=$(realtime "$KANZI" -c -t None -b "$B" -e "$E" -i "$INPUT" -o "$TMP/e.knz" -j "$JOBS" -f)
      o=$(fsize "$TMP/e.knz")
      dt=$(realtime "$KANZI" -d -i "$TMP/e.knz" -o "$TMP/e.out" -j "$JOBS" -f)
      printf "%-26s %-10s %-12s %-12s\n" "kanzi $E N=$B" "$(frac "$o")" "$(mbps "$ct")" "$(mbps "$dt")"
      rm -f "$TMP/e.knz" "$TMP/e.out"
    done
	done
fi

# Kanzi: other codecs with the transform disabled.
if [ -n "$KANZI" ] && [ -x "$KANZI" ]; then
	for E in "${OTHER_CODECS[@]}"; do
	  for B in "${OTHER_BLOCKS[@]}"; do
      ct=$(realtime "$KANZI" -c -t None -b "$B" -e "$E" -i "$INPUT" -o "$TMP/e.knz" -j "$JOBS" -f)
      o=$(fsize "$TMP/e.knz")
      dt=$(realtime "$KANZI" -d -i "$TMP/e.knz" -o "$TMP/e.out" -j "$JOBS" -f)
      printf "%-26s %-10s %-12s %-12s\n" "kanzi $E N=$B" "$(frac "$o")" "$(mbps "$ct")" "$(mbps "$dt")"
      rm -f "$TMP/e.knz" "$TMP/e.out"
    done
	done
fi
