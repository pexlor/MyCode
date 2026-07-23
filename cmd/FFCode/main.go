package main

import (
	"MyCode/internal/repl"
	"fmt"
	"io"
	"os"
)

// version can be replaced during release builds with:
// go build -ldflags "-X main.version=vX.Y.Z"
var version = "0.1.0"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, repl.REPL))
}

func run(arguments []string, stdout, stderr io.Writer, startREPL func()) int {
	if len(arguments) == 0 {
		startREPL()
		return 0
	}
	if len(arguments) != 1 {
		fmt.Fprintln(stderr, "error: expected at most one option")
		printUsage(stderr)
		return 2
	}
	switch arguments[0] {
	case "--help", "-h":
		printUsage(stdout)
		return 0
	case "--version", "-v":
		fmt.Fprintf(stdout, "MyCode %s\n", version)
		return 0
	default:
		fmt.Fprintf(stderr, "error: unknown option %q\n", arguments[0])
		printUsage(stderr)
		return 2
	}
}

func printUsage(out io.Writer) {
	fmt.Fprint(out, `MyCode - terminal coding assistant

Usage:
  MyCode [option]

Options:
  -h, --help       Show this help message
  -v, --version    Show the MyCode version
`)
}
