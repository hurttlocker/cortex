package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/hurttlocker/cortex/internal/codexrollout"
)

func main() {
	res, err := codexrollout.Execute(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(res.Output)
	if res.ExitCode != 0 {
		os.Exit(res.ExitCode)
	}
}
