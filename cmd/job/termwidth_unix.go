//go:build unix

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// fileWidth asks the terminal behind f for its width; ok is false when
// f is not a terminal.
func fileWidth(f *os.File) (int, bool) {
	ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return 0, false
	}
	return int(ws.Col), true
}
