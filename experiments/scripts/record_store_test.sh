#!/usr/bin/env bash
#
# Record-store experiment (paper: the "homogeneous record stores" use case).
# Compresses a file of fixed-width records at the record granularity --- block =
# record width --- in both modes, with gzip as context. The point: at record
# granularity stream mode's alphabet amortisation is decisive. Per-record mode pays
# the full alphabet (cardinality + rank over C(256,c)) on EVERY small record, which
# can dwarf the payload and even expand it; stream mode names the shared alphabet
# once, so each record pays almost nothing for it.
#
# Two gzip points are reported, because they bracket the access trade-off:
#   gzip (whole stream)  -- best ratio, but NO per-record random access or encryption
#   gzip (per record)    -- access-preserving, but pays a ~18-byte header per record
# The method's stream mode sits between: per-record access AND a competitive ratio.
#
# Usage:
#   INPUT=data/records_ts.bin WIDTH=24 experiments/scripts/record_store_test.sh
# Environment overrides:
#   INPUT    fixed-width records file   (default data/records_ts.bin)
#   WIDTH    record width = block      (default 24)
#   JOBS     concurrent jobs            (default 8)
#   PERREC_GZIP_SAMPLE  records to sample for the (slow) per-record gzip estimate
#                       (default 2000; set 0 to skip)
#   VERIFY   1 = round-trip the codec runs (default 0)
set -euo pipefail

INPUT=${INPUT:-data/records_ts.bin}
WIDTH=${WIDTH:-24}
JOBS=${JOBS:-8}
PERREC_GZIP_SAMPLE=${PERREC_GZIP_SAMPLE:-2000}
VERIFY=${VERIFY:-0}

# Locate the module root (the directory containing go.mod) by walking up.
ROOT=$(cd "$(dirname "$0")" && pwd)
while [ "$ROOT" != "/" ] && [ ! -f "$ROOT/go.mod" ]; do ROOT=$(dirname "$ROOT"); done
[ -f "$ROOT/go.mod" ] || { echo "could not find go.mod above $(dirname "$0")" >&2; exit 1; }
[ -f "$INPUT" ] || { echo "input not found: $INPUT (set INPUT=...; run recordgen.sh first?)" >&2; exit 1; }
INPUT="$(cd "$(dirname "$INPUT")" && pwd)/$(basename "$INPUT")" # absolute
SIZE=$(wc -c <"$INPUT" | tr -d ' ')

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
go build -o "$TMP/ajz" "$ROOT"
cp "$INPUT" "$TMP/in"

frac() { awk -v n="$1" -v d="$SIZE" 'BEGIN { printf "%.4f", n / d }'; }
fsize() { wc -c <"$1" | tr -d ' '; }

codec_size() { # <flag>
	"$TMP/ajz" -k $1 -b "$WIDTH" -j "$JOBS" "$TMP/in" >/dev/null 2>&1
	local o; o=$(fsize "$TMP/in.ajz")
	if [ "$VERIFY" = 1 ]; then
		cp "$TMP/in.ajz" "$TMP/d.ajz"
		"$TMP/ajz" -d -k -j "$JOBS" "$TMP/d.ajz" 2>&1 | tr '\r' '\n' | grep -aq 'hashes agree' \
			|| echo "  WARNING: round-trip failed (mode=${1:-stream})" >&2
		rm -f "$TMP/d" "$TMP/d.ajz"
	fi
	rm -f "$TMP/in.ajz"
	echo "$o"
}

stream=$(codec_size "")
perrec=$(codec_size "-noref")
gzip_whole=$(gzip -9 -c "$TMP/in" | wc -c | tr -d ' ')

echo "input: $INPUT"
echo "records: $((SIZE / WIDTH)) x $WIDTH bytes = $SIZE bytes total, jobs: $JOBS"
echo
printf "%-26s %-14s %-10s\n" "method (per-record access?)" "bytes" "fraction"
printf "%-26s %-14s %-10s\n" "codec, stream     (yes)" "$stream" "$(frac "$stream")"
printf "%-26s %-14s %-10s\n" "codec, per-record (yes)" "$perrec" "$(frac "$perrec")"
printf "%-26s %-14s %-10s\n" "gzip -9, whole    (NO)"  "$gzip_whole" "$(frac "$gzip_whole")"

# Access-preserving gzip: gzip each record on its own. A full run spawns one gzip
# process per record (far too slow at scale), so we sample and extrapolate; the
# per-record overhead is a near-constant header, so a small sample is representative.
if [ "$PERREC_GZIP_SAMPLE" -gt 0 ]; then
	nrec=$((SIZE / WIDTH))
	k=$((PERREC_GZIP_SAMPLE < nrec ? PERREC_GZIP_SAMPLE : nrec))
	head -c $((k * WIDTH)) "$TMP/in" >"$TMP/sample"
	acc=0
	for ((i = 0; i < k; i++)); do
		c=$(dd if="$TMP/sample" bs="$WIDTH" skip="$i" count=1 2>/dev/null | gzip -9 -c | wc -c | tr -d ' ')
		acc=$((acc + c))
	done
	est=$(awk -v acc="$acc" -v k="$k" -v nrec="$nrec" 'BEGIN { printf "%d", acc * nrec / k }')
	printf "%-26s %-14s %-10s  (est. from %d-record sample)\n" \
		"gzip -9, per record (yes)" "$est" "$(frac "$est")" "$k"
fi
