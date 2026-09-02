//go:build linux

package console

import (
	"fmt"
	"os/exec"
)

// chooseStorageDirectory keeps the console path-free on common Linux desktop environments. We intentionally use system chooser tools rather than a browser file input: the agent, not the browser, must be able to access the selected directory when it performs the copy.
func chooseStorageDirectory() (string, error) {
	if _, err := exec.LookPath("zenity"); err == nil {
		output, chooseErr := exec.Command("zenity", "--file-selection", "--directory", "--title=Choose EveryAPI model directory").Output()
		if chooseErr != nil {
			return "", fmt.Errorf("choose storage directory: %w", chooseErr)
		}
		return pickedStorageDirectory(output)
	}
	if _, err := exec.LookPath("kdialog"); err == nil {
		output, chooseErr := exec.Command("kdialog", "--getexistingdirectory", ".").Output()
		if chooseErr != nil {
			return "", fmt.Errorf("choose storage directory: %w", chooseErr)
		}
		return pickedStorageDirectory(output)
	}
	return "", fmt.Errorf("%w; install zenity or kdialog", errPickerUnavailable)
}
