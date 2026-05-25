package main

import "fmt"

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
	}
}
