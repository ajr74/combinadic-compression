#!/usr/bin/env bash
#
# Generate a synthetic "record store": N fixed-width records over a narrow, shared
# alphabet, concatenated with no separators so that block = record. The default
# emits ISO-8601 timestamps --- "YYYY-MM-DDTHH:MM:SS.mmmZ", exactly 24 bytes each,
# over the 15-symbol alphabet {0-9, '-', 'T', ':', '.', 'Z'} --- a ubiquitous
# database / log column type. Records are random within a date range (so they carry
# realistic per-record diversity), but the alphabet is fixed and shared across every
# record, which is the property a record store has and that stream mode exploits.
# Deterministic given SEED.
#
# Usage:
#   N=1000000 SEED=1 OUT=data/records_ts.bin experiments/scripts/recordgen.sh
# Then:
#   INPUT=data/records_ts.bin WIDTH=24 experiments/scripts/record_store_test.sh
#
# To use REAL data instead, turn any one-record-per-line file (e.g. a CSV column cut
# out with `cut -d, -fK`) into fixed-width records and feed that to the runner:
#   awk -v W=24 '{ r=substr($0,1,W); while(length(r)<W) r=r" "; printf "%s",r }' \
#       column.txt > data/records_real.bin
# (right-pads/truncates each value to W bytes; pick W to match the field.)
set -euo pipefail

N=${N:-1000000}
SEED=${SEED:-1}
OUT=${OUT:-data/records_ts.bin}

awk -v n="$N" -v seed="$SEED" 'BEGIN {
	srand(seed)
	for (i = 0; i < n; i++) {
		Y = 2020 + int(rand() * 5)   # 2020..2024
		M = 1 + int(rand() * 12)
		D = 1 + int(rand() * 28)
		h = int(rand() * 24); m = int(rand() * 60); s = int(rand() * 60)
		ms = int(rand() * 1000)
		printf "%04d-%02d-%02dT%02d:%02d:%02d.%03dZ", Y, M, D, h, m, s, ms
	}
}' >"$OUT"

echo "wrote $OUT: $N records x 24 bytes = $((N * 24)) bytes (alphabet: 15 symbols)" >&2
