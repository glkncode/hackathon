//go:build !windows

package service

import "os"

func protectDocumentDirectory(root *os.Root) error { return root.Chmod(".", 0700) }
