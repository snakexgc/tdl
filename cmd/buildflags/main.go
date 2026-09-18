// buildflags prints the shared release linker flags without starting tdl.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/snakexgc/tdl/internal/buildflags"
)

func main() {
	version := flag.String("version", "dev", "release version")
	commit := flag.String("commit", "unknown", "source commit")
	date := flag.String("date", "unknown", "commit date")
	arm := flag.String("arm", "", "GOARM variant")
	flag.Parse()
	flags, err := buildflags.Render(*version, *commit, *date, *arm)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(flags)
}
