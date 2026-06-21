package main

import (
	"fmt"
	"os"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		if _, printErr := fmt.Fprintln(os.Stderr, err); printErr != nil {
			os.Exit(1)
		}
		os.Exit(1)
	}
}
