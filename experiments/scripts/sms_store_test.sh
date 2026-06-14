#!/usr/bin/env bash
#
# SMS record-store experiment (paper: the "homogeneous record stores" use case,
# A2P / two-factor messages). Compresses a file of fixed-width SMS records at the
# record granularity --- block = record width --- in both modes, against the
# baselines that bracket the access trade-off.
#
# The headline baseline is GSM-7 packing: the GSM 03.38 air-interface encoding
# stores in-alphabet text at a flat 7 bits/char (160 chars per 140-byte segment),
# i.e. a fixed fraction of 7/8 = 0.875 of 1-byte-per-char storage, regardless of
# content. It is the SMS analogue of 2-bit packing for nucleotides: a fixed-width
# scheme that ignores the character-frequency skew that exact order-0 ranking
# captures. The method should beat 0.875 on natural-language / templated text by
# exactly the margin its order-0 entropy sits below 7 bits/char.
#
# Coders reported (all but whole-stream gzip preserve per-record access):
#   codec, stream      -- shared alphabet named once; per-record access + encryption
#   codec, per-record  -- each record self-contained; pays the full alphabet each time
#   GSM-7 packing      -- the fixed-width 0.875 baseline (access-preserving)
#   gzip -9, per record-- access-preserving but a ~18-byte header per tiny record
#   gzip -9, whole     -- best ratio, but NO per-record access/encryption (a ceiling)
#
# Usage (smsgen.sh prints this line with the right WIDTH):
#   INPUT=data/sms_otp.bin WIDTH=55 experiments/scripts/sms_store_test.sh
# Environment overrides:
#   INPUT    fixed-width records file   (default data/sms_otp.bin)
#   WIDTH    record width = block      (default 55, the 'medium' template)
#   JOBS     concurrent jobs            (default 8)
#   PERREC_GZIP_SAMPLE  records to sample for the (slow) per-record gzip estimate
#                       (default 2000; set 0 to skip)
#   VERIFY   1 = round-trip the codec runs (default 0)
set -euo pipefail

INPUT=${INPUT:-data/sms_otp.bin}
WIDTH=${WIDTH:-55}
JOBS=${JOBS:-8}
PERREC_GZIP_SAMPLE=${PERREC_GZIP_SAMPLE:-2000}
VERIFY=${VERIFY:-0}

# Locate the module root (the directory containing go.mod) by walking up.
ROOT=$(cd "$(dirname "$0")" && pwd)
while [ "$ROOT" != "/" ] && [ ! -f "$ROOT/go.mod" ]; do ROOT=$(dirname "$ROOT"); done
[ -f "$ROOT/go.mod" ] || { echo "could not find go.mod above $(dirname "$0")" >&2; exit 1; }
[ -f "$INPUT" ] || { echo "input not found: $INPUT (set INPUT=...; run smsgen.sh first?)" >&2; exit 1; }
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
# GSM-7 packing: 7 bits per in-alphabet char => ceil(WIDTH*7/8) bytes per record.
gsm7=$(awk -v n="$SIZE" -v w="$WIDTH" 'BEGIN { per = int((w * 7 + 7) / 8); printf "%d", (n / w) * per }')

echo "input: $INPUT"
echo "records: $((SIZE / WIDTH)) x $WIDTH bytes = $SIZE bytes total, jobs: $JOBS"
echo
printf "%-28s %-14s %-10s\n" "method (per-record access?)" "bytes" "fraction"
printf "%-28s %-14s %-10s\n" "codec, stream      (yes)" "$stream"     "$(frac "$stream")"
printf "%-28s %-14s %-10s\n" "codec, per-record  (yes)" "$perrec"     "$(frac "$perrec")"
printf "%-28s %-14s %-10s\n" "GSM-7 packing      (yes)" "$gsm7"       "$(frac "$gsm7")"
printf "%-28s %-14s %-10s\n" "gzip -9, whole     (NO)"  "$gzip_whole" "$(frac "$gzip_whole")"

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
	printf "%-28s %-14s %-10s  (est. from %d-record sample)\n" \
		"gzip -9, per record (yes)" "$est" "$(frac "$est")" "$k"
fi
