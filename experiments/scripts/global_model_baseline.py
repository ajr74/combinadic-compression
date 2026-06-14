#!/usr/bin/env python3
"""Idealised globally-modelled order-0 baseline (the "fair" matched-block reference).

Builds ONE byte-frequency model over the whole stream, then charges each block its
cross-entropy against that global model, byte-aligned and with the same 2-byte
per-block length prefix the .ajz format carries, so every block stays independently
decodable (the per-record-access requirement). This is the counterpart the matched-
block comparison lacks: a coder granted the SAME global model-sharing scope our stream
mode takes, unlike a per-block-resetting coder (e.g. Kanzi). It is a lower bound for any
real static global-model order-0 coder (arithmetic/range), and is reported as an ideal.

Requires numpy. Usage:
    python3 global_model_baseline.py FILE [blocksize ...]
e.g.  python3 experiments/scripts/global_model_baseline.py data/enwik9 512 1024 2048 4096 8192
"""
import sys
import numpy as np

FRAME = 2  # per-block length prefix in bytes, matching the .ajz framing the codec pays


def main():
    if len(sys.argv) < 2:
        print(__doc__); sys.exit(1)
    path = sys.argv[1]
    blocks = [int(x) for x in sys.argv[2:]] or [512, 1024, 2048, 4096, 8192]
    data = np.fromfile(path, dtype=np.uint8)
    n = data.size
    hist = np.bincount(data, minlength=256).astype(np.float64)
    p = hist / n
    cost = np.zeros(256)
    nz = p > 0
    cost[nz] = -np.log2(p[nz])          # bits to code each byte value under the global model
    per_byte = cost[data]
    model = int(np.count_nonzero(hist)) * 2   # global table, transmitted once (negligible)
    H = per_byte.sum() / n
    print(f"{path}: {n} bytes, alphabet {int(np.count_nonzero(hist))}, "
          f"global H0={H:.4f} bits/byte (entropy floor {H/8:.4f})")
    for N in blocks:
        nb = (n + N - 1) // N
        pad = nb * N - n
        pb = np.concatenate([per_byte, np.zeros(pad)]) if pad else per_byte
        block_bytes = np.ceil(pb.reshape(nb, N).sum(axis=1) / 8.0)   # byte-aligned per block
        total = block_bytes.sum() + nb * FRAME + model
        print(f"   N={N:>5}: global-model order-0 fraction = {total / n:.4f}")


if __name__ == "__main__":
    main()
