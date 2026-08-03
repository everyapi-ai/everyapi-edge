//go:build !darwin

package console

import "fmt"

func chooseStorageDirectory() (string, error) {
	return "", fmt.Errorf("use the target directory field to choose a path on this platform")
}
