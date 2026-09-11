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

  A local package directory (-FromDirectory) is the same path with a different
  source: nothing is downloaded, the checksums come from the package's own
  SHA256SUMS, and everything after verification is identical. That is what lets
  a test environment exercise the installer a production machine will run.

  Doing it by hand — fetching, checking the sum, running
  `.\jobfinder.exe install`, expanding the extension zip — is equivalent; this
  only saves those steps.

.PARAMETER Extension
  Install only the extension, not the backend.

.PARAMETER All
  Install the backend and the extension.

.PARAMETER Version
  Install a specific release instead of the latest.

.PARAMETER FromDirectory
  Install from a local package directory instead of a release. Its SHA256SUMS is
  still verified, and the version comes from the artifact names inside it.

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
    [string]$FromDirectory = "",
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

# A local package carries its own version and checksums, so the flag that
# selects a release has nothing left to select.
$fromDir = ""
if (-not [string]::IsNullOrWhiteSpace($FromDirectory)) {
    if (-not [string]::IsNullOrWhiteSpace($Version)) { throw "-FromDirectory and -Version cannot be combined" }
    if (-not (Test-Path -LiteralPath $FromDirectory -PathType Container)) { throw "no such directory: $FromDirectory" }
    $fromDir = (Resolve-Path -LiteralPath $FromDirectory).Path
}

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
# file. It returns the path of the fetched archive.
function Get-Artifact([string]$Base, [string]$Work, [string]$Artifact) {
    $path = Join-Path $Work $Artifact
    if ($script:fromDir) {
        Write-Step "reading $Artifact"
        $source = Join-Path $script:fromDir $Artifact
        if (-not (Test-Path -LiteralPath $source -PathType Leaf)) { throw "no such artifact: $source" }
        Copy-Item -LiteralPath $source -Destination $path -Force
    } else {
        Write-Step "downloading $Artifact"
        Invoke-WebRequest -Uri "$Base/$Artifact" -OutFile $path -UseBasicParsing
    }

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
    # The Mark of the Web on a downloaded zip is inherited by everything
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
"@

    # A release manifest carries a fixed key, so its ID is the same everywhere
    # and can be printed here. A package built for testing has no key — Chrome
    # derives the ID from the load directory, so only Chrome can tell you it.
    if ((Get-Content -LiteralPath $manifest -Raw) -match '"key"') {
        Write-Host @"

The extension ID is fixed by the manifest key:
  $extensionId
The backend only answers requests from it once its config carries
  api.extension_origin: chrome-extension://$extensionId
"@
    } else {
        Write-Host @"

This package has no manifest key, so Chrome derives the extension ID from the
directory above. Read the ID off chrome://extensions after loading it, then put
it in the backend config as
  api.extension_origin: chrome-extension://<the id Chrome shows>
Moving the directory changes the ID, so leave it where it is.
"@
    }

    if ($Keep) { Write-Host "`nArchive kept at $archive" }
}

function Invoke-Bootstrap {
    $work = (New-Item -ItemType Directory -Path (Join-Path $env:TEMP ("jobfinder-bootstrap-" + [guid]::NewGuid().ToString("N")))).FullName
    $sums = Join-Path $work "SHA256SUMS"
    $base = ""
    $tag = $Version

    try {
        if ($script:fromDir) {
            $localSums = Join-Path $script:fromDir "SHA256SUMS"
            if (-not (Test-Path -LiteralPath $localSums -PathType Leaf)) { throw "no SHA256SUMS in $script:fromDir" }
            Copy-Item -LiteralPath $localSums -Destination $sums -Force
            # The package names its own version: every artifact carries it, and
            # the extension zip is the one present in every package regardless
            # of platform.
            $tag = ""
            foreach ($line in Get-Content -LiteralPath $sums) {
                if ($line -match 'jobfinder-extension_(.+)\.zip\s*$') { $tag = $matches[1]; break }
            }
            if ([string]::IsNullOrWhiteSpace($tag)) { throw "could not derive the version from $localSums" }
            Write-Step "installing $tag from $script:fromDir"
        } else {
            if ([string]::IsNullOrWhiteSpace($tag)) {
                Write-Step "resolving the latest release of $Repo"
                $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -UseBasicParsing
                $tag = $release.tag_name
                if ([string]::IsNullOrWhiteSpace($tag)) { throw "could not resolve the latest release tag" }
            }
            $base = "https://github.com/$Repo/releases/download/$tag"
            Invoke-WebRequest -Uri "$base/SHA256SUMS" -OutFile $sums -UseBasicParsing
        }

        if ($mode -ne "extension") { Install-Backend $base $work $tag }
        if ($mode -ne "backend") { Install-Extension $base $work $tag }
    }
    finally {
        if (-not $Keep) { Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue }
    }
}

Invoke-Bootstrap
