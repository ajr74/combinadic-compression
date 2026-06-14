#!/usr/bin/env bash
#
# Generate a synthetic A2P (application-to-person) SMS store: N fixed-width
# messages from a single sender, concatenated with no separators so that
# block = record. Only a numeric field (an OTP code) varies per record; the
# surrounding template is fixed, the alphabet fixed and shared across every
# record --- the property a real A2P store has, and the SMS counterpart of the
# fixed-width timestamp store of recordgen.sh. Deterministic given SEED.
#
# TEMPLATE selects the message (all fixed-width, in the GSM-7 alphabet):
#   medium  "NNNNNN is your one-time passcode, valid for 10 minutes."   55 bytes (default)
#   short   "Your verification code is NNNNNN"                          32 bytes
# 'short' is the boundary case (a bare code) on which the codec LOSES to GSM-7
# packing; 'medium' is a representative real A2P message on which it WINS. Running
# both reproduces the length-dependent crossover.
#
# Usage:
#   N=1000000 SEED=1 TEMPLATE=medium OUT=data/sms_otp.bin experiments/scripts/smsgen.sh
# The script prints the exact follow-up command (with the right WIDTH).
#
# To use REAL SMS instead, turn any one-message-per-line file into fixed-width
# records (right-pad/truncate each to W bytes with a space, an in-alphabet filler):
#   awk -v W=64 '{ r=substr($0,1,W); while(length(r)<W) r=r" "; printf "%s",r }' \
#       messages.txt > data/sms_real.bin
# (variable-length corpora are better measured per-record; padding inflates the raw
# size with a single, highly compressible symbol and is not a faithful store model.)
set -euo pipefail

N=${N:-1000000}
SEED=${SEED:-1}
TEMPLATE=${TEMPLATE:-medium}
OUT=${OUT:-data/sms_otp.bin}

case "$TEMPLATE" in
	medium) FMT="%06d is your one-time passcode, valid for 10 minutes."; WIDTH=55 ;;
	short)  FMT="Your verification code is %06d";                        WIDTH=32 ;;
	*) echo "unknown TEMPLATE=$TEMPLATE (use 'medium' or 'short')" >&2; exit 1 ;;
esac

mkdir -p "$(dirname "$OUT")"

awk -v n="$N" -v seed="$SEED" -v fmt="$FMT" 'BEGIN {
	srand(seed)
	for (i = 0; i < n; i++) printf fmt, int(rand() * 1000000)
}' >"$OUT"

got=$(wc -c <"$OUT" | tr -d ' ')
echo "wrote $OUT: $N records x $WIDTH bytes = $got bytes (template=$TEMPLATE)" >&2
echo "next:  INPUT=$OUT WIDTH=$WIDTH experiments/scripts/sms_store_test.sh" >&2
