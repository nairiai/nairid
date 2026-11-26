//go:build windows

package retention

import "io/fs"

func onlyWeCanChange(fs.FileInfo) error {
	return nil
}
