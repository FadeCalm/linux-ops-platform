//go:build unix

package api

import (
	"os"
	"syscall"
)

func fileOwner(info os.FileInfo) (uid, gid uint32) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Uid, st.Gid
	}
	return 0, 0
}
