#!/bin/bash
# Run with "caffeinate" to prevent sleep.
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
export KANZI=${KANZI:-kanzi}
export INPUT=${INPUT:-"$ROOT/data/silesia.bin"}

mkdir -p ./tmp
for i in 01 02 03 04 05 06 07 08 09 10; do
  ./enwik9_test.sh > "./tmp/silesia_results_${i}.txt"
done
