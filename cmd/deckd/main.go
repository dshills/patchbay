package main

import (
	"os"
	"patchbay/internal/cli"
)

func main() { os.Exit(cli.Run("deckd", os.Args[1:], os.Stdout, os.Stderr)) }
