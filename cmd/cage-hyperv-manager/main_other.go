//go:build !windows

package main

import (
	"fmt"
	"os"
)

func main() { fmt.Fprintln(os.Stderr, "Windows host manager requires Windows."); os.Exit(1) }
