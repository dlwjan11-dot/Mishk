//go:build !windows && !darwin

package main

import "fmt"

func main() { fmt.Println("Mishk runs on Windows.") }
