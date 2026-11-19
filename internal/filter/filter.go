// Package filter decides which files and folders the satellite may look at.
package filter

import (
	"io/fs"
	"strings"
)

// Same ballast list as the server's path_rejected check, so the satellite never
// sends what the server would refuse.
var ballastNames = map[string]bool{
	"$recycle.bin":              true,
	"system volume information": true,
	"thumbs.db":                 true,
	"desktop.ini":               true,
	".ds_store":                 true,
	"pagefile.sys":              true,
	"hiberfil.sys":              true,
	"swapfile.sys":              true,
}

func IsBallast(name string) bool {
	lower := strings.ToLower(name)
	return ballastNames[lower] || strings.HasPrefix(lower, "~$")
}

// Skip reports whether a file or folder entry must be ignored.
func Skip(name string, info fs.FileInfo) bool {
	return IsBallast(name) || isHidden(name, info)
}
