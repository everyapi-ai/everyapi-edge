package console

import (
	"errors"
	"fmt"
	"strings"
)

// errPickerUnavailable marks the one picker failure that is a property of the host rather than of a chosen path: no native chooser exists at all. Its message names the remedy and contains no filesystem path, so the console may show it verbatim instead of the redacted generic failure every other picker error gets.
var errPickerUnavailable = errors.New("no native directory picker is available")

// pickedStorageDirectory normalizes the result returned by each platform's native directory chooser. A blank result is a cancellation, never a usable filesystem path.
func pickedStorageDirectory(output []byte) (string, error) {
	path := strings.TrimSpace(string(output))
	if path == "" {
		return "", fmt.Errorf("no storage directory was selected")
	}
	return path, nil
}
