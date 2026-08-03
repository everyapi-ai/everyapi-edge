//go:build windows

package console

import (
	"fmt"
	"os/exec"
)

func chooseStorageDirectory() (string, error) {
	const chooseDirectory = `$dialog = New-Object System.Windows.Forms.FolderBrowserDialog; $dialog.Description = 'Choose EveryAPI model directory'; if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { [Console]::Write($dialog.SelectedPath) }`
	output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "Add-Type -AssemblyName System.Windows.Forms; "+chooseDirectory).Output()
	if err != nil {
		return "", fmt.Errorf("choose storage directory: %w", err)
	}
	return pickedStorageDirectory(output)
}
