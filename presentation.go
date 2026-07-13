package main

import (
	"fmt"
	"strings"
)

func printHeader(name string, category int) {
	options := map[int]int{0: 90, 1: 60, 2: 30}
	chars, ok := options[category]
	if !ok {
		chars = 90
	}

	fmt.Println()
	fmt.Println(strings.Repeat("#", chars))
	fmt.Println("#" + center(name, chars-2) + "#")
	fmt.Println(strings.Repeat("#", chars))
}

func center(s string, width int) string {
	if len(s) >= width {
		return s
	}
	left := (width - len(s)) / 2
	right := width - len(s) - left
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", right)
}

func maskedCommandLine(args []string) string {
	var out []string
	wasPassword := false
	for _, arg := range args {
		if wasPassword {
			out = append(out, "xxxxxxx")
		} else {
			out = append(out, arg)
		}
		wasPassword = arg == "-dp"
	}
	return strings.Join(out, " ")
}
