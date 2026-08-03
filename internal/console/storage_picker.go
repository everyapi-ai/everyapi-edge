package console

import (
	"fmt"
	"strings"
)

// pickedStorageDirectory normalizes the result returned by each platform's
// native directory chooser. A blank result is a cancellation, never a usable
// filesystem path.
func pickedStorageDirectory(output []byte) (string, error) {
	path := strings.TrimSpace(string(output))
	if path == "" {
		return "", fmt.Errorf("no storage directory was selected")
	}
	return path, nil
}
