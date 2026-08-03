//go:build !darwin && !linux && !windows

package console

import "fmt"

func chooseStorageDirectory() (string, error) {
	return "", fmt.Errorf("native directory picker is not available on this platform")
}
