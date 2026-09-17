// Command zlib searches and downloads books from Z-Library and Anna's Archive.
//
// It is a single static binary: the EAPI client, the Anna's Archive scraper,
// and the CLI are all implemented here in Go, with no Python or Node runtime
// required.
package main

import (
	"os"

	"github.com/difyz9/zlib-go/cmd"
)

func main() {
	os.Exit(cmd.Run(os.Args[1:]))
}
