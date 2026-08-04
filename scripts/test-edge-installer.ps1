$ErrorActionPreference = "Stop"
Import-Module (Join-Path $PSScriptRoot "EdgeInstaller.psm1") -Force

if ($IsWindows) {
    $successCommand = "cmd.exe"
    $successArguments = @("/c", "echo ready")
    $failureArguments = @("/c", "exit 7")
} else {
    $successCommand = "/bin/sh"
    $successArguments = @("-c", "printf ready")
    $failureArguments = @("-c", "exit 7")
}

$output = (Invoke-CheckedNative $successCommand $successArguments | Out-String).Trim()
if ($output -ne "ready") {
    throw "checked native helper lost stdout: $output"
}

$failed = $false
try {
    Invoke-CheckedNative $successCommand $failureArguments | Out-Null
} catch {
    $failed = $_.Exception.Message.Contains("exit code 7")
}
if (-not $failed) {
    throw "checked native helper did not stop on exit code 7"
}

Write-Output "PowerShell native command checks passed"
