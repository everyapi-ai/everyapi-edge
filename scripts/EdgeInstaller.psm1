function Invoke-CheckedNative {
    param(
        [Parameter(Mandatory = $true)][string]$Command,
        [Parameter()][string[]]$Arguments = @()
    )

    $output = & $Command @Arguments
    $exitCode = $LASTEXITCODE
    if ($exitCode -ne 0) {
        throw "$Command failed with exit code $exitCode"
    }
    return $output
}

Export-ModuleMember -Function Invoke-CheckedNative
