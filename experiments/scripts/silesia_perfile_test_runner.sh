#!/bin/bash
# Run with "caffeinate" to prevent sleep.
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
export KANZI=${KANZI:-kanzi}
export DIR=${DIR:-"$ROOT/data/silesia"}
export BLOCK=8192

mkdir -p ./tmp
./silesia_perfile.sh > ./tmp/silesia_perfile_results_8192.txt
