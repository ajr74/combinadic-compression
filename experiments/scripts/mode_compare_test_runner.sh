#!/bin/bash
# Run with "caffeinate" to prevent sleep.
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
export INPUT=${INPUT:-"$ROOT/data/enwik9"}

"$ROOT/experiments/scripts/mode_compare.sh" > "$ROOT/experiments/results/enwik9_mode_compare_results.txt"
