package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"patchbay/internal/demo"
	"patchbay/internal/version"
)

func main() {
	showVersion := flag.Bool("version", false, "show build version")
	jsonOutput := flag.Bool("json", false, "use JSON version output")
	iterations := flag.Int("iterations", 10000, "SHA-256 iterations (1–100000)")
	repeats := flag.Int("repeats", 5, "number of repeats (2–20)")
	flag.Parse()
	if *showVersion {
		if *jsonOutput {
			if err := json.NewEncoder(os.Stdout).Encode(version.Current()); err != nil {
				os.Exit(1)
			}
		} else {
			fmt.Println(version.Current())
		}
		return
	}
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
