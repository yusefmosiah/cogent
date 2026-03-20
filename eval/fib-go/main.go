package main

import (
	"fmt"
	"os"
	"strconv"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <N>\n", os.Args[0])
		os.Exit(1)
	}
	n, err := strconv.Atoi(os.Args[1])
	if err != nil || n < 0 {
		fmt.Fprintf(os.Stderr, "Error: N must be a non-negative integer\n")
		os.Exit(1)
	}
	for _, v := range Fibonacci(n) {
		fmt.Println(v)
	}
}
