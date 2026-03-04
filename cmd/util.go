package cmd

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/term"
)

// readPassword reads a password from stdin without echoing it.
func readPassword(prompt string) string {
	fmt.Print(prompt)
	if term.IsTerminal(int(syscall.Stdin)) {
		pw, err := term.ReadPassword(int(syscall.Stdin))
		fmt.Println()
		if err != nil {
			return ""
		}
		return string(pw)
	}
	// non-interactive: just read a line
	var pw string
	fmt.Fscan(os.Stdin, &pw)
	return pw
}
