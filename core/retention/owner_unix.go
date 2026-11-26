//go:build !windows

package retention

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

// onlyWeCanChange reports an error unless the folder belongs to this process's user and no one
// else may write to it, so no one else can swap what sits inside it.
func onlyWeCanChange(info fs.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("has no owner information")
	}
	if int(stat.Uid) != os.Getuid() {
		return errors.New("belongs to another user")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return errors.New("is writable by other users")
	}
	return nil
}
