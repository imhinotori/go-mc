package tui

import (
	"os"

	"golang.org/x/term"
)

// IsTerminal reports whether the given file descriptor is attached to a terminal.
// It is the pure-Go (no cgo) TTY probe Plan 02's main() uses to decide between the
// interactive TUI path and the plain-stderr (headless/Docker) path. Bubbletea does
// NOT self-degrade on a non-TTY, so this guard is ours.
func IsTerminal(fd uintptr) bool {
	return term.IsTerminal(int(fd))
}

// StdoutIsTerminal reports whether the process's stdout is a terminal.
func StdoutIsTerminal() bool {
	return IsTerminal(os.Stdout.Fd())
}
