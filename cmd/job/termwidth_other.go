//go:build !unix

package main

import "os"

// fileWidth has no portable answer off unix; callers fall back to the
// default width.
func fileWidth(*os.File) (int, bool) { return 0, false }
