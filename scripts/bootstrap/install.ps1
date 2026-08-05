<#
.SYNOPSIS
  jobfinder bootstrap for Windows.

.DESCRIPTION
  This script only fetches: it downloads a release artifact, verifies it against
  the release's SHA256SUMS, unpacks it, and hands over to `jobfinder install`.
  Every installation decision — paths, token generation, config rendering, task
  registration, verification — lives in that subcommand, so this script and its
  shell counterpart stay thin and cannot drift apart from each other.

  Downloading the zip by hand and running `.\jobfinder.exe install` yourself is
  equivalent; this only saves the download and the checksum check.

.PARAMETER Version
  Install a specific release instead of the latest.

.PARAMETER Keep
  Leave the unpacked artifact in place and print where it is.

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File install.ps1
#>
[CmdletBinding()]
param(
    [string]$Version = "",
    [string]$Repo = "dccoding1118/job-finder",
    [switch]$Keep
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest
# TLS 1.2 is not the default on the Windows PowerShell 5.1 that ships with the
# OS, and GitHub refuses anything older.
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

function Write-Step([string]$Message) { Write-Host "==> $Message" -ForegroundColor Cyan }

if ([string]::IsNullOrWhiteSpace($Version)) {
    Write-Step "resolving the latest release of $Repo"
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -UseBasicParsing
    $Version = $release.tag_name
    if ([string]::IsNullOrWhiteSpace($Version)) { throw "could not resolve the latest release tag" }
}

$name = "jobfinder_${Version}_windows_amd64"
$artifact = "$name.zip"
$base = "https://github.com/$Repo/releases/download/$Version"
$work = New-Item -ItemType Directory -Path (Join-Path $env:TEMP ("jobfinder-bootstrap-" + [guid]::NewGuid().ToString("N")))

try {
    Write-Step "downloading $artifact"
    Invoke-WebRequest -Uri "$base/$artifact" -OutFile (Join-Path $work $artifact) -UseBasicParsing
    Invoke-WebRequest -Uri "$base/SHA256SUMS" -OutFile (Join-Path $work "SHA256SUMS") -UseBasicParsing

    $expected = $null
    foreach ($line in Get-Content (Join-Path $work "SHA256SUMS")) {
        $fields = $line -split '\s+', 2
        if ($fields.Count -eq 2 -and $fields[1].Trim().TrimStart('*', './') -eq $artifact) {
            $expected = $fields[0].Trim()
            break
        }
    }
    if (-not $expected) { throw "$artifact is not listed in SHA256SUMS" }
    $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $work $artifact)).Hash
    if ($expected -ne $actual.ToLower() -and $expected.ToLower() -ne $actual.ToLower()) {
        throw "checksum mismatch for ${artifact}: expected $expected, got $actual"
    }
    Write-Step "checksum verified"

    Expand-Archive -LiteralPath (Join-Path $work $artifact) -DestinationPath $work -Force
    $unpacked = Join-Path $work $name
    $exe = Join-Path $unpacked "jobfinder.exe"
    if (-not (Test-Path -LiteralPath $exe)) { throw "the artifact does not contain an executable at $exe" }

    Write-Step "handing over to jobfinder install"
    & $exe install
    if ($LASTEXITCODE -ne 0) { throw "jobfinder install exited with $LASTEXITCODE" }

    if ($Keep) { Write-Host "`nUnpacked artifact kept at $unpacked" }
}
finally {
    if (-not $Keep) { Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue }
}
