//go:build windows

package filter

import (
	"io/fs"
	"syscall"
)

func isHidden(_ string, info fs.FileInfo) bool {
	attrs, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return false
	}
	return attrs.FileAttributes&(syscall.FILE_ATTRIBUTE_HIDDEN|syscall.FILE_ATTRIBUTE_SYSTEM) != 0
}
