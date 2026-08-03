//go:build darwin

package console

import (
	"fmt"
	"os/exec"
)

// chooseStorageDirectory runs on the supplier's desktop, so macOS can return
// a real absolute path rather than the privacy-preserving relative filename a
// browser file input exposes.
func chooseStorageDirectory() (string, error) {
	output, err := exec.Command("osascript", "-e", `POSIX path of (choose folder with prompt "Choose EveryAPI model directory")`).Output()
	if err != nil {
		return "", fmt.Errorf("choose storage directory: %w", err)
	}
	return pickedStorageDirectory(output)
}
