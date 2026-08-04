# EveryAPI Edge installer for Windows NVIDIA hosts.
# Requires Windows 10/11, Docker Desktop using the WSL2 Linux backend, and an
# NVIDIA GPU visible to both Windows and Docker Desktop.
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][long]$NodeId,
    [string]$Token = "",
    [string]$Gateway = "https://api.everyapi.ai",
    [string]$Name = $env:COMPUTERNAME,
    [string]$Model = "qwen2.5:3b",
    [string]$InstallDir = (Join-Path $HOME "everyapi-edge")
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
if ($NodeId -le 0) { throw "node id must be a positive integer" }
if ($Gateway -notmatch '^https://[A-Za-z0-9._:-]+(/[A-Za-z0-9._~:/?@%+=,-]*)?$') {
    throw "gateway must use HTTPS"
}
if ($Token -and $Token -notmatch '^edgert_[A-Za-z0-9_-]+$') {
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
if ((Test-Path -LiteralPath $identityPath) -and (Get-Item $identityPath).Length -gt 0) {
    $Token = ""
} elseif (-not $Token) {
    throw "registration token is required for a new node"
}

$modelRoot = (Join-Path $HOME ".everyapi/edge").Replace('\', '/')
$imageModelRoot = (Join-Path $modelRoot "images").Replace('\', '/')
New-Item -ItemType Directory -Force -Path $modelRoot, $imageModelRoot | Out-Null
$envPath = Join-Path $InstallDir ".env"
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
    "EVERYAPI_DIFFUSERS_URL=http://diffusers:8188"
)
[IO.File]::WriteAllLines($temporaryEnv, $envLines, [Text.UTF8Encoding]::new($false))
Move-Item -LiteralPath $temporaryEnv -Destination $envPath -Force

Push-Location $InstallDir
try {
    Invoke-CheckedNative docker @("compose", "-f", $composeFile, "pull", "--ignore-buildable")
    Invoke-CheckedNative docker @("compose", "-f", $composeFile, "build", "diffusers")
    $logSince = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
    Invoke-CheckedNative docker @("compose", "-f", $composeFile, "up", "-d")
    Wait-AgentConnection $logSince

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
    Pop-Location
}

Write-Host "EveryAPI Edge is online on Windows with $gpuModel and image model support."
