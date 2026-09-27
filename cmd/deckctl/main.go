package main

import (
	"os"
	"patchbay/internal/cli"
)

func main() { os.Exit(cli.Run("deckctl", os.Args[1:], os.Stdout, os.Stderr)) }
