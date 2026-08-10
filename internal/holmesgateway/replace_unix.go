//go:build !windows

package holmesgateway

import "os"

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}
