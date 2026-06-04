package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	versionFlag := flag.Bool("version", false, "print version information")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of calyx:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if *versionFlag {
		fmt.Println("v0.0.0")
		os.Exit(0)
	}

	if len(os.Args) == 1 {
		flag.Usage()
		os.Exit(0)
	}
}
