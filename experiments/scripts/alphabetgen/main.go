// Command alphabetgen writes `size` bytes of uniform random values drawn from
// the alphabet {0, 1, ..., A-1} to a file. It generates the controlled inputs
// for the alphabet-size experiment reported in doc/experiments.tex.
//
// Usage:
//
//	alphabetgen <size> <alphabet> <output>
package main

import (
	"fmt"
	"math/rand"
	"os"
	"strconv"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: alphabetgen <size> <alphabet> <output>")
		os.Exit(2)
	}
	size, err := strconv.Atoi(os.Args[1])
	if err != nil || size < 0 {
		fmt.Fprintln(os.Stderr, "size must be a non-negative integer")
		os.Exit(2)
	}
	a, err := strconv.Atoi(os.Args[2])
	if err != nil || a < 1 || a > 256 {
		fmt.Fprintln(os.Stderr, "alphabet must be an integer in [1, 256]")
		os.Exit(2)
	}

	// Deterministic per alphabet so results are reproducible.
	rng := rand.New(rand.NewSource(int64(a)*7 + 1))
	buf := make([]byte, size)
	for i := range buf {
		buf[i] = byte(rng.Intn(a))
	}
	if err := os.WriteFile(os.Args[3], buf, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
		os.Exit(1)
	}
}
