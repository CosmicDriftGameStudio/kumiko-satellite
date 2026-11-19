//go:build !windows

package filter

import (
	"io/fs"
	"strings"
)

func isHidden(name string, _ fs.FileInfo) bool {
	return strings.HasPrefix(name, ".")
}
