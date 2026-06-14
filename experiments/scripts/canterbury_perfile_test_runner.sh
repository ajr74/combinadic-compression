#!/bin/bash
# Run with "caffeinate" to prevent sleep.
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
export KANZI=${KANZI:-kanzi}
export DIR=${DIR:-"$ROOT/data/canterbury_large"}
export BLOCK=1024

mkdir -p ./tmp
./silesia_perfile.sh > ./tmp/canterbury_large_perfile_results_1024.txt
