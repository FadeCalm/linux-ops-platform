//go:build !unix

package api

import "os"

// fileOwner is a no-op on non-unix platforms (used for local development).
func fileOwner(info os.FileInfo) (uid, gid uint32) {
	return 0, 0
}
