package main

import "os"

// File log sources are not supported on Windows: no inode to follow a rotation.
func fileKey(os.FileInfo) (string, bool) {
	return "", false
}
