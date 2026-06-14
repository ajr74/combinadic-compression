#!/bin/bash
# Run with "caffeinate" to prevent sleep, e.g.:
#   caffeinate -d -i -m -u ./enwik9_test_runner.sh
# KANZI: path to a Kanzi binary (or leave as 'kanzi' if on PATH).
# INPUT: the enwik9 file. Defaults are relative to the repo root.
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
export KANZI=${KANZI:-kanzi}
export INPUT=${INPUT:-"$ROOT/data/enwik9"}

for i in 01 02 03 04 05 06 07 08 09 10; do
  ./enwik9_test.sh > "enwik9_test_results_this_work_only_${i}.txt"
done
