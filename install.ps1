# EveryAPI Edge installer for Windows NVIDIA hosts.
# Requires Windows 10/11, Docker Desktop using the WSL2 Linux backend, and an
# NVIDIA GPU visible to both Windows and Docker Desktop.
[CmdletBinding()]
param(
    [long]$NodeId = 0,
    [string]$Token = "",
    [string]$Gateway = "https://api.everyapi.ai",
    [string]$Name = $env:COMPUTERNAME,
    [string]$Model = "qwen2.5:3b",
    [string]$InstallDir = (Join-Path $HOME "everyapi-edge"),
    [switch]$Uninstall,
    [switch]$PurgeModels
)

$ErrorActionPreference = "Stop"
$bundleSource = "https://github.com/everyapi-ai/everyapi-edge"
$composeFile = "docker-compose.windows.yml"
Import-Module (Join-Path $PSScriptRoot "scripts/EdgeInstaller.psm1") -Force

function Assert-SingleLine([string]$Label, [string]$Value) {
    if ($Value.Contains("`r") -or $Value.Contains("`n")) {
        throw "$Label must not contain line breaks"
    }
}

function Require-Command([string]$Name) {
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "$Name is required"
    }
}

function Wait-AgentConnection([string]$Since) {
    $deadline = (Get-Date).AddSeconds(60)
    while ((Get-Date) -lt $deadline) {
        $logs = (& docker logs --since $Since everyapi-edge-agent 2>&1 | Out-String)
        if ($logs.Contains("connected to gateway")) {
            return
        }
        if ($logs -match "session ended:|fatal:|authentication failed|node revoked") {
            throw "agent connection failed:`n$logs"
        }
        Start-Sleep -Seconds 1
    }
    throw "agent did not connect to the gateway within 60 seconds"
}

foreach ($entry in @{
    "gateway" = $Gateway
    "registration token" = $Token
    "node name" = $Name
    "model" = $Model
    "install directory" = $InstallDir
}.GetEnumerator()) {
    Assert-SingleLine $entry.Key $entry.Value
}
if ($PurgeModels -and -not $Uninstall) { throw "-PurgeModels requires -Uninstall" }
$canonicalHome = [IO.Path]::GetFullPath($HOME).TrimEnd('\', '/')
$canonicalInstall = [IO.Path]::GetFullPath($InstallDir).TrimEnd('\', '/')
if (-not $canonicalInstall -or $canonicalInstall -eq [IO.Path]::GetPathRoot($canonicalInstall).TrimEnd('\', '/') -or $canonicalInstall -eq $canonicalHome) {
    throw "refusing unsafe install directory: $InstallDir"
}
if ($Uninstall) {
    $modelRoot = Join-Path $HOME ".everyapi/edge"
    if (-not (Test-Path -LiteralPath $canonicalInstall)) {
        if ($PurgeModels -and (Test-Path -LiteralPath $modelRoot)) { Remove-Item -LiteralPath $modelRoot -Recurse -Force }
        Write-Host "EveryAPI Edge is already uninstalled"
        return
    }
    if ((Get-Item -LiteralPath $canonicalInstall).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw "refusing a symlink or junction install directory" }
    Require-Command git
    Require-Command docker
    if (-not (Test-Path -LiteralPath (Join-Path $canonicalInstall ".git"))) { throw "refusing existing non-EveryAPI directory: $canonicalInstall" }
    $origin = (Invoke-CheckedNative git @("-C", $canonicalInstall, "remote", "get-url", "origin") | Out-String).Trim()
    if ($origin -notin @($bundleSource, "$bundleSource.git")) { throw "refusing an existing directory that is not an official Edge checkout" }
    foreach ($candidate in @("docker-compose.windows.yml", "docker-compose.yml", "docker-compose.rocm.yml", "docker-compose.macos.yml")) {
        if (Test-Path -LiteralPath (Join-Path $canonicalInstall $candidate)) {
            try { Push-Location $canonicalInstall; Invoke-CheckedNative docker @("compose", "-f", $candidate, "down", "--remove-orphans") } catch { Write-Warning $_ } finally { Pop-Location }
        }
    }
    Remove-Item -LiteralPath $canonicalInstall -Recurse -Force
    if ($PurgeModels -and (Test-Path -LiteralPath $modelRoot)) { Remove-Item -LiteralPath $modelRoot -Recurse -Force } else { Write-Host "Preserved models at $modelRoot" }
    Write-Host "EveryAPI Edge uninstalled"
    return
}
if ($NodeId -le 0) { throw "node id must be a positive integer" }
if ($Gateway -notmatch '^https://[A-Za-z0-9._:-]+(/[A-Za-z0-9._~:/?@%+=,-]*)?$') {
    throw "gateway must use HTTPS"
}
if ($Token -and $Token -notmatch '^(edgert|edgerekey)_[A-Za-z0-9_-]+$') {
    throw "registration token has an invalid format"
}
if ($Name -notmatch '^[A-Za-z0-9._ -]{1,128}$') {
    throw "node name contains unsupported characters"
}
if ($Model -notmatch '^[A-Za-z0-9._:/-]+$') {
    throw "model contains unsupported characters"
}

Require-Command git
Require-Command docker
Require-Command nvidia-smi
Invoke-CheckedNative docker @("compose", "version") | Out-Null
$dockerOS = (Invoke-CheckedNative docker @("info", "--format", "{{.OSType}}") | Out-String).Trim()
if ($dockerOS -ne "linux") {
    throw "Docker Desktop must use its WSL2 Linux-container backend"
}
$gpuModel = (Invoke-CheckedNative nvidia-smi @("--query-gpu=name", "--format=csv,noheader") | Select-Object -First 1).Trim()
$vramMiB = (Invoke-CheckedNative nvidia-smi @("--query-gpu=memory.total", "--format=csv,noheader,nounits") | Select-Object -First 1).Trim()
if ($vramMiB -notmatch '^\d+$') { throw "could not determine NVIDIA GPU memory" }
$vramGB = [math]::Ceiling([int64]$vramMiB / 1024)

if (Test-Path -LiteralPath $InstallDir) {
    if (-not (Test-Path -LiteralPath (Join-Path $InstallDir ".git"))) {
        throw "refusing existing non-EveryAPI directory: $InstallDir"
    }
    $origin = (Invoke-CheckedNative git @("-C", $InstallDir, "remote", "get-url", "origin") | Out-String).Trim()
    if ($origin -notin @($bundleSource, "$bundleSource.git")) {
        throw "refusing an existing directory that is not an official Edge checkout"
    }
    Invoke-CheckedNative git @("-C", $InstallDir, "pull", "--ff-only")
} else {
    $parent = Split-Path -Parent $InstallDir
    if (-not (Test-Path -LiteralPath $parent)) {
        throw "install directory parent does not exist: $parent"
    }
    Invoke-CheckedNative git @("clone", "--depth", "1", "--", $bundleSource, $InstallDir)
}

$identityPath = Join-Path $InstallDir "data/agent/identity.json"
$rekeyPending = $false
if ((Test-Path -LiteralPath $identityPath) -and (Get-Item $identityPath).Length -gt 0) {
    if ($Token.StartsWith("edgerekey_")) {
        if (Test-Path -LiteralPath "$identityPath.rekey-backup") { Remove-Item -LiteralPath $identityPath -Force } else { Move-Item -LiteralPath $identityPath -Destination "$identityPath.rekey-backup" }
        $rekeyPending = $true
        Remove-Item -LiteralPath (Join-Path (Split-Path -Parent $identityPath) ".revoked") -Force -ErrorAction SilentlyContinue
    } else {
        $Token = ""
    }
} elseif (-not $Token) {
    throw "registration token is required for a new node"
}
if ($Token.StartsWith("edgerekey_") -and (Test-Path -LiteralPath "$identityPath.rekey-backup")) { $rekeyPending = $true }

$modelRoot = (Join-Path $HOME ".everyapi/edge").Replace('\', '/')
$imageModelRoot = (Join-Path $modelRoot "images").Replace('\', '/')
$speechModelRoot = (Join-Path $modelRoot "speech").Replace('\', '/')
$transcriptionModelRoot = (Join-Path $modelRoot "transcription").Replace('\', '/')
$videoModelRoot = (Join-Path $modelRoot "video").Replace('\', '/')
$rerankModelRoot = (Join-Path $modelRoot "rerank").Replace('\', '/')
New-Item -ItemType Directory -Force -Path $modelRoot, $imageModelRoot, $speechModelRoot, $transcriptionModelRoot, $videoModelRoot, $rerankModelRoot | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $InstallDir "data/render-workflows"), (Join-Path $InstallDir "data/render") | Out-Null
$envPath = Join-Path $InstallDir ".env"
$consoleToken = ""
if (Test-Path -LiteralPath $envPath) {
    foreach ($line in Get-Content -LiteralPath $envPath) {
        if ($line.StartsWith("EVERYAPI_CONSOLE_TOKEN=")) {
            $consoleToken = $line.Substring("EVERYAPI_CONSOLE_TOKEN=".Length)
            break
        }
    }
}
if ($consoleToken -and $consoleToken -notmatch '^[A-Za-z0-9_-]{32,128}$') {
    throw "console token has an invalid format"
}
if (-not $consoleToken) {
    $consoleToken = New-EdgeConsoleToken
}
$temporaryEnv = Join-Path $InstallDir (".env." + [guid]::NewGuid().ToString("N") + ".tmp")
$envLines = @(
    "EVERYAPI_GATEWAY=$Gateway",
    "EVERYAPI_NODE_ID=$NodeId",
    "EVERYAPI_REGISTRATION_TOKEN=$Token",
    "EVERYAPI_NODE_NAME=$Name",
    "EVERYAPI_GPU_MODEL=$gpuModel",
    "EVERYAPI_VRAM_GB=$vramGB",
    "EVERYAPI_PLATFORM=windows/amd64",
    "EVERYAPI_MODEL_PATH=$modelRoot",
    "EVERYAPI_IMAGE_MODEL_PATH=$imageModelRoot",
    "EVERYAPI_SPEECH_MODEL_PATH=$speechModelRoot",
    "EVERYAPI_TRANSCRIPTION_MODEL_PATH=$transcriptionModelRoot",
    "EVERYAPI_VIDEO_MODEL_PATH=$videoModelRoot",
    "EVERYAPI_RERANK_MODEL_PATH=$rerankModelRoot",
    "EVERYAPI_DIFFUSERS_URL=http://diffusers:8188",
    "EVERYAPI_SPEECH_URL=http://speech:8189",
    "EVERYAPI_TRANSCRIPTION_URL=http://transcription:8190",
    "EVERYAPI_VIDEO_URL=http://video:8191",
    "EVERYAPI_RENDER_URL=http://render:8192",
    "EVERYAPI_RERANK_URL=http://rerank:8193",
    "EVERYAPI_RENDER_WORKFLOW_PATH=./data/render-workflows",
    "EVERYAPI_CONSOLE_TOKEN=$consoleToken"
)
[IO.File]::WriteAllLines($temporaryEnv, $envLines, [Text.UTF8Encoding]::new($false))
Move-Item -LiteralPath $temporaryEnv -Destination $envPath -Force

Push-Location $InstallDir
try {
    Invoke-CheckedNative docker @("compose", "-f", $composeFile, "pull", "--ignore-buildable")
    Invoke-CheckedNative docker @("compose", "-f", $composeFile, "build", "diffusers", "speech", "transcription", "video", "render", "rerank")
    $logSince = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
    Invoke-CheckedNative docker @("compose", "-f", $composeFile, "up", "-d")
    Wait-AgentConnection $logSince
    if ($rekeyPending -and (Test-Path -LiteralPath "$identityPath.rekey-backup")) { Remove-Item -LiteralPath "$identityPath.rekey-backup" -Force; $rekeyPending = $false }

    if ($Token) {
        $envLines = $envLines | Where-Object { -not $_.StartsWith("EVERYAPI_REGISTRATION_TOKEN=") }
        [IO.File]::WriteAllLines($temporaryEnv, $envLines, [Text.UTF8Encoding]::new($false))
        Move-Item -LiteralPath $temporaryEnv -Destination $envPath -Force
    }

    Invoke-CheckedNative docker @("compose", "-f", $composeFile, "exec", "-T", "ollama", "ollama", "pull", $Model)
    $logSince = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
    Invoke-CheckedNative docker @("compose", "-f", $composeFile, "up", "-d", "--force-recreate", "agent")
    Wait-AgentConnection $logSince
} finally {
    if ($rekeyPending -and (Test-Path -LiteralPath "$identityPath.rekey-backup")) { Remove-Item -LiteralPath $identityPath -Force -ErrorAction SilentlyContinue; Move-Item -LiteralPath "$identityPath.rekey-backup" -Destination $identityPath -Force }
    Pop-Location
}

Write-Host "EveryAPI Edge is online on Windows with $gpuModel and image model support."
Write-Host "Edge Control Room pairing token: $consoleToken"
