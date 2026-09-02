//go:build !darwin && !linux && !windows

package console

import "fmt"

func chooseStorageDirectory() (string, error) {
	return "", fmt.Errorf("%w on this platform", errPickerUnavailable)
}
