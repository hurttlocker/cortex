package main

import (
	"fmt"
	"os"

	"github.com/hurttlocker/cortex/internal/codexrollout"
)

func main() {
	res, err := codexrollout.Execute(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(res.Output)
	if res.ExitCode != 0 {
		os.Exit(res.ExitCode)
	}
}
