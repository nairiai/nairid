//go:build windows

package retention

import (
	"errors"
	"io/fs"
)

// onlyWeCanChange cannot tell on Windows who else may change the folder, so a shared folder is
// never pruned there.
func onlyWeCanChange(fs.FileInfo) error {
	return errors.New("cannot be checked for other writers on Windows")
}
