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

$firstToken = New-EdgeConsoleToken
$secondToken = New-EdgeConsoleToken
if ($firstToken -notmatch '^[a-f0-9]{64}$' -or $secondToken -notmatch '^[a-f0-9]{64}$') {
    throw "console token helper did not return 32 random bytes as lowercase hexadecimal"
}
if ($firstToken -eq $secondToken) {
    throw "console token helper returned the same value twice"
}

Write-Output "PowerShell native command checks passed"
