<#
.SYNOPSIS
  jobfinder bootstrap for Windows.

.DESCRIPTION
  This script only fetches. For the backend it downloads a release artifact,
  verifies it against the release's SHA256SUMS, unpacks it and hands over to
  `jobfinder install` (or `jobfinder update` when an installation is already
  there). Every installation decision — paths, token generation, config
  rendering, task registration, verification — lives in those subcommands, so
  this script and its shell counterpart stay thin and cannot drift apart from
  each other.

  The extension has no installation semantics at all: nothing is rendered, no
  token is issued, no task is registered. Its whole path is therefore this
  script — download the extension artifact, verify it, unpack it into a
  resident directory, and print the Chrome steps that have no command-line
  entry point.

  Doing it by hand — downloading, checking the sum, running
  `.\jobfinder.exe install`, expanding the extension zip — is equivalent; this
  only saves those steps.

.PARAMETER Extension
  Install only the extension, not the backend.

.PARAMETER All
  Install the backend and the extension.

.PARAMETER Version
  Install a specific release instead of the latest.

.PARAMETER Keep
  Leave the downloads in place and print where they are.

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File install.ps1

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File install.ps1 -Extension
#>
[CmdletBinding()]
param(
    [switch]$Extension,
    [switch]$All,
    [string]$Version = "",
    [string]$Repo = "dccoding1118/job-finder",
    [switch]$Keep
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest
# TLS 1.2 is not the default on the Windows PowerShell 5.1 that ships with the
# OS, and GitHub refuses anything older.
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

# The extension ID is a constant: manifest.json carries a fixed key, so Chrome
# derives the same ID on every machine and for every version.
$extensionId = "oddnhajjhmgogefocnljofeahniodiei"

# The two mode switches select one mode between them, so asking for both at
# once is a contradiction rather than a last-one-wins.
if ($Extension -and $All) { throw "-Extension and -All cannot be combined" }
$mode = if ($Extension) { "extension" } elseif ($All) { "both" } else { "backend" }

function Write-Step([string]$Message) { Write-Host "==> $Message" -ForegroundColor Cyan }

function Get-LocalAppData {
    if ([string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
        return (Join-Path $HOME "AppData\Local")
    }
    return $env:LOCALAPPDATA
}

# The release writes its sums with `sha256sum ./*.tar.gz ./*.zip`, so every name
# carries a leading "./"; GNU coreutils also marks binary mode with a leading
# "*". Both are prefixes on the line, not part of the artifact name.
function Get-ExpectedChecksum([string[]]$Lines, [string]$Artifact) {
    foreach ($line in $Lines) {
        $fields = $line -split '\s+', 2
        if ($fields.Count -ne 2) { continue }
        if (($fields[1].Trim() -replace '^(\*|\./)', '') -eq $Artifact) { return $fields[0].Trim() }
    }
    return $null
}

# Get-Artifact verifies before it returns, so no caller can reach an unverified
# file. It returns the path of the downloaded archive.
function Get-Artifact([string]$Base, [string]$Work, [string]$Artifact) {
    Write-Step "downloading $Artifact"
    $path = Join-Path $Work $Artifact
    Invoke-WebRequest -Uri "$Base/$Artifact" -OutFile $path -UseBasicParsing

    $expected = Get-ExpectedChecksum (Get-Content (Join-Path $Work "SHA256SUMS")) $Artifact
    if (-not $expected) { throw "$Artifact is not listed in SHA256SUMS" }
    $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $path).Hash
    if ($expected.ToLower() -ne $actual.ToLower()) {
        throw "checksum mismatch for ${Artifact}: expected $expected, got $actual"
    }
    Write-Step "checksum verified: $Artifact"
    return $path
}

function Install-Backend([string]$Base, [string]$Work, [string]$Tag) {
    $name = "jobfinder_${Tag}_windows_amd64"
    $archive = Get-Artifact $Base $Work "$name.zip"

    Expand-Archive -LiteralPath $archive -DestinationPath $Work -Force
    $unpacked = Join-Path $Work $name
    # The Mark of the Web on the downloaded zip is inherited by everything
    # expanded out of it, and SmartScreen blocks a marked executable.
    Get-ChildItem -Recurse -File -LiteralPath $unpacked | Unblock-File
    $exe = Join-Path $unpacked "jobfinder.exe"
    if (-not (Test-Path -LiteralPath $exe)) { throw "the artifact does not contain an executable at $exe" }

    # The resident binary is the one existing-install marker both subcommands
    # agree on: `update` refuses without it, `install` is what creates it.
    $resident = Join-Path (Get-LocalAppData) "jobfinder\bin\jobfinder.exe"
    $subcommand = if (Test-Path -LiteralPath $resident) { "update" } else { "install" }

    Write-Step "handing over to jobfinder $subcommand"
    & $exe $subcommand
    if ($LASTEXITCODE -ne 0) { throw "jobfinder $subcommand exited with $LASTEXITCODE" }

    if ($Keep) { Write-Host "`nUnpacked artifact kept at $unpacked" }
}

function Install-Extension([string]$Base, [string]$Work, [string]$Tag) {
    $archive = Get-Artifact $Base $Work "jobfinder-extension_${Tag}.zip"

    # The resident directory follows the same local app data root the backend
    # resolves, and is per-tag so an older unpack stays intact until Chrome
    # points at the new one.
    $dest = (New-Item -ItemType Directory -Force -Path (Join-Path (Get-LocalAppData) "jobfinder\extension\$Tag")).FullName
    Expand-Archive -LiteralPath $archive -DestinationPath $dest -Force
    Get-ChildItem -Recurse -File -LiteralPath $dest | Unblock-File
    $manifest = Join-Path $dest "manifest.json"
    if (-not (Test-Path -LiteralPath $manifest)) { throw "the artifact does not contain a manifest at $manifest" }
    Write-Step "extension unpacked at $dest"

    # Chrome has no command-line entry point for loading an unpacked extension,
    # so the script stops at a prepared directory and the steps to point Chrome
    # at it. Removing the old card and refilling Options stay manual.
    Write-Host @"

The extension directory is ready. Finish in Chrome:
  1. open chrome://extensions and turn on Developer mode
  2. remove the previous jobfinder card, if there is one
  3. Load unpacked -> $dest
  4. Details -> Extension options: fill in the API endpoint and token

The extension ID is fixed by the manifest key:
  $extensionId
The backend only answers requests from it once its config carries
  api.extension_origin: chrome-extension://$extensionId
"@

    if ($Keep) { Write-Host "`nDownloaded archive kept at $archive" }
}

function Invoke-Bootstrap {
    $tag = $Version
    if ([string]::IsNullOrWhiteSpace($tag)) {
        Write-Step "resolving the latest release of $Repo"
        $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -UseBasicParsing
        $tag = $release.tag_name
        if ([string]::IsNullOrWhiteSpace($tag)) { throw "could not resolve the latest release tag" }
    }

    $base = "https://github.com/$Repo/releases/download/$tag"
    $work = (New-Item -ItemType Directory -Path (Join-Path $env:TEMP ("jobfinder-bootstrap-" + [guid]::NewGuid().ToString("N")))).FullName

    try {
        Invoke-WebRequest -Uri "$base/SHA256SUMS" -OutFile (Join-Path $work "SHA256SUMS") -UseBasicParsing

        if ($mode -ne "extension") { Install-Backend $base $work $tag }
        if ($mode -ne "backend") { Install-Extension $base $work $tag }
    }
    finally {
        if (-not $Keep) { Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue }
    }
}

Invoke-Bootstrap
