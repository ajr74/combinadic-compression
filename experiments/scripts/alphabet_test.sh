#!/usr/bin/env bash
#
# Alphabet-size experiment for doc/experiments.tex. Compress `SIZE` bytes of
# uniform random data drawn from an A-symbol alphabet and report, per alphabet:
#
#   MODE=ratio (default)  compressed fraction at two block sizes, next to the
#                         theoretical order-0 entropy (1/8)*log2(A) and Huffman.
#   MODE=time             self-reported compression throughput (MB/s), which
#                         excludes the codec's one-time InitCache so it is a
#                         fair kernel-vs-kernel comparison against Huffman.
#
# Usage:
#   experiments/alphabet_test.sh
#   MODE=time SIZE=268435456 KANZI=/path/to/Kanzi experiments/alphabet_test.sh
# Environment overrides:
#   MODE       ratio | time            (default ratio)
#   SIZE       input size in bytes     (default 67108864 = 64 MiB)
#   ALPHABETS  space-separated A values (default "2 4 16 64 256")
#   KANZI      path to a Kanzi binary  (Huffman column shown if set & executable)
#
# Requires a Go toolchain (and awk). The codec is built with the go-bitarray
# linkname flag; the generator builds plainly.
set -euo pipefail

MODE=${MODE:-ratio}
SIZE=${SIZE:-67108864}
ALPHABETS=${ALPHABETS:-"2 4 16 64 256"}
KANZI=${KANZI:-kanzi}

# Locate the module root (the directory containing go.mod) by walking up from this
# script, so it works wherever it sits under the repo (experiments/, experiments/scripts/, ...).
ROOT=$(cd "$(dirname "$0")" && pwd)
while [ "$ROOT" != "/" ] && [ ! -f "$ROOT/go.mod" ]; do ROOT=$(dirname "$ROOT"); done
[ -f "$ROOT/go.mod" ] || { echo "could not find go.mod above $(dirname "$0")" >&2; exit 1; }
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

go build -o "$TMP/ajz" "$ROOT"
go build -o "$TMP/gen" "$ROOT/experiments/alphabetgen"

fsize() { wc -c <"$1" | tr -d ' '; }
frac() { awk -v n="$1" -v d="$SIZE" 'BEGIN { printf "%.4f", n / d }'; }
# MB/s (MiB per second) from a seconds (float) or milliseconds (int) duration.
mbps_s() { awk -v sz="$SIZE" -v t="$1" 'BEGIN { printf "%.0f", (sz / 1048576) / t }'; }
mbps_ms() { awk -v sz="$SIZE" -v ms="$1" 'BEGIN { printf "%.0f", (sz / 1048576) / (ms / 1000) }'; }

# ajz self-reported compression: prints fraction (ratio mode) or MB/s (time mode).
ajz_run() { # block file
	"$TMP/ajz" -k -b "$1" -j 8 "$2" >"$TMP/log" 2>&1 || true
	if [ "$MODE" = time ]; then
		t=$(tr '\r' '\n' <"$TMP/log" | grep -a 'Compression time:' | sed -E 's/.*: ([0-9.]+)s/\1/')
		mbps_s "$t"
	else
		frac "$(fsize "$2.ajz")"
	fi
}
# Kanzi Huffman (transform=None): prints fraction or MB/s; "-" if no Kanzi binary.
kanzi_run() { # file
	[ -n "$KANZI" ] && [ -x "$KANZI" ] || { echo "-"; return; }
	out=$("$KANZI" -c -t None -e Huffman -i "$1" -o "$TMP/a.knz" -j 8 -f 2>&1)
	if [ "$MODE" = time ]; then
		ms=$(echo "$out" | grep -aoE 'in [0-9]+ ms' | grep -oE '[0-9]+')
		mbps_ms "$ms"
	else
		frac "$(fsize "$TMP/a.knz")"
	fi
}

if [ "$MODE" = time ]; then
	printf "%-4s %-14s %-14s %-12s\n" "A" "ajz N=1024" "ajz N=4096" "Huffman"
	unit="MB/s"
else
	printf "%-4s %-10s %-12s %-12s %-12s\n" "A" "order0/8" "ajz N=1024" "ajz N=4096" "Kanzi Huffman"
fi

for A in $ALPHABETS; do
	"$TMP/gen" "$SIZE" "$A" "$TMP/a.dat"
	cp "$TMP/a.dat" "$TMP/w1"
	r1=$(ajz_run 1024 "$TMP/w1")
	cp "$TMP/a.dat" "$TMP/w4"
	r4=$(ajz_run 4096 "$TMP/w4")
	rk=$(kanzi_run "$TMP/a.dat")

	if [ "$MODE" = time ]; then
		printf "%-4s %-14s %-14s %-12s\n" "$A" "$r1 $unit" "$r4 $unit" "$rk $unit"
	else
		o0=$(awk -v a="$A" 'BEGIN { printf "%.4f", (log(a) / log(2)) / 8 }')
		printf "%-4s %-10s %-12s %-12s %-12s\n" "$A" "$o0" "$r1" "$r4" "$rk"
	fi
	rm -f "$TMP"/a.dat "$TMP"/w1 "$TMP"/w1.ajz "$TMP"/w4 "$TMP"/w4.ajz "$TMP"/a.knz
done
