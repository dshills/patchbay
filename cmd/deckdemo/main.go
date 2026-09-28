package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"patchbay/internal/demo"
)

func main() {
	iterations := flag.Int("iterations", 10000, "SHA-256 iterations (1–100000)")
	repeats := flag.Int("repeats", 5, "number of repeats (2–20)")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "deckdemo accepts only --iterations and --repeats")
		os.Exit(2)
	}
	result, err := demo.Run(*iterations, *repeats)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		os.Exit(1)
	}
}
