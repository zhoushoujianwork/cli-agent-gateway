package main

import (
	"fmt"
	"os"
)

func runDeprecatedCLI(action, replacement string, jsonOut bool) int {
	message := fmt.Sprintf("%s is deprecated; use `%s`", action, replacement)
	if jsonOut {
		printJSONActionError(action, "deprecated_command", message)
		return 1
	}
	fmt.Fprintln(os.Stderr, message)
	return 1
}
