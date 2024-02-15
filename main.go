package main

import (
	"os"

	flags "github.com/jessevdk/go-flags"
)

// Options specifies the options of the main program
type Options struct {
}

var options Options
var parser = flags.NewParser(&options, flags.Default)
var relay bool

// function executions are handled by jessevdk/go-flags
// the respective init() functions and Execute() methods
// launch the subcommands

func main() {
	if _, err := parser.Parse(); err != nil {
		switch flagsErr := err.(type) {
		case flags.ErrorType:
			if flagsErr == flags.ErrHelp {
				os.Exit(0)
			}
			os.Exit(1)
		default:
			os.Exit(1)
		}
	}
}
