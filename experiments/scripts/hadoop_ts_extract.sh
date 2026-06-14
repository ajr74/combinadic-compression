#!/usr/bin/env bash
#
# Extract the ISO-8601 timestamp column from the loghub Hadoop dataset into a
# fixed-width record store (block = record), for the real-data record-store
# experiment (paper Section "A record store"). Every log line that begins with a
# "YYYY-MM-DD HH:MM:SS,mmm" timestamp contributes one 23-byte record; continuation
# and stack-trace lines (which carry no leading timestamp) are skipped. Records are
# concatenated with no separators so that block = record, exactly as recordgen.sh
# does for synthetic timestamps.
#
# Data source: loghub Hadoop dataset (LOGPAI), https://github.com/logpai/loghub
# (download the full Hadoop set from the loghub Zenodo archive and unzip into SRC).
#
# Usage:
#   SRC=data/Hadoop OUT=data/records_hadoop_ts.bin experiments/scripts/hadoop_ts_extract.sh
# Then:
#   INPUT=data/records_hadoop_ts.bin WIDTH=23 VERIFY=1 experiments/scripts/record_store_test.sh
set -euo pipefail

SRC=${SRC:-data/Hadoop}
OUT=${OUT:-data/records_hadoop_ts.bin}

[ -d "$SRC" ] || { echo "source dir not found: $SRC (unzip the loghub Hadoop dataset there)" >&2; exit 1; }
mkdir -p "$(dirname "$OUT")"

find "$SRC" -name '*.log' -exec \
	grep -hoE '^[0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2},[0-9]{3}' {} + \
	| tr -d '\n' >"$OUT"

B=$(wc -c <"$OUT" | tr -d ' ')
[ "$B" -gt 0 ] || { echo "no timestamps extracted from $SRC" >&2; exit 1; }
echo "wrote $OUT: $B bytes = $((B / 23)) records x 23 bytes (real loghub Hadoop timestamps)" >&2
echo "next:  INPUT=$OUT WIDTH=23 VERIFY=1 experiments/scripts/record_store_test.sh" >&2
