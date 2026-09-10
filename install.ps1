<#
.SYNOPSIS
    Installs flighttrack for the current user.

.DESCRIPTION
    Builds the binary (if Go is available), copies it to
    %LOCALAPPDATA%\Programs\flighttrack, and adds that folder to your user PATH
    so you can run `flighttrack` from any terminal.

    Everything happens under your own user account. No administrator rights are
    needed, the machine-wide PATH is never touched, and nothing is written
    outside your profile.

.PARAMETER Uninstall
    Remove the installed binary and take the folder back off your PATH.
    Your saved search history is left alone unless -Purge is also given.

.PARAMETER Purge
    With -Uninstall, also delete %APPDATA%\flighttrack (your search history).

.PARAMETER NoPath
    Install the binary but leave PATH alone.

.EXAMPLE
    .\install.ps1

.EXAMPLE
    .\install.ps1 -Uninstall -Purge
#>
[CmdletBinding()]
param(
    [switch]$Uninstall,
    [switch]$Purge,
    [switch]$NoPath
)

$ErrorActionPreference = 'Stop'

$AppName   = 'flighttrack'
$ExeName   = 'flighttrack.exe'
$InstallTo = Join-Path $env:LOCALAPPDATA "Programs\$AppName"
$DataDir   = Join-Path $env:APPDATA $AppName
$SourceDir = $PSScriptRoot

function Write-Step($msg) { Write-Host "  $msg" }
function Write-Good($msg) { Write-Host "  $msg" -ForegroundColor Green }
function Write-Warn($msg) { Write-Host "  $msg" -ForegroundColor Yellow }

# --- PATH helpers ------------------------------------------------------------
# Only ever reads and writes the *user* PATH. The machine PATH needs admin
# rights and is shared with every account, so it is deliberately untouched.

function Get-UserPathEntries {
    $raw = [Environment]::GetEnvironmentVariable('Path', 'User')
    if ([string]::IsNullOrEmpty($raw)) { return @() }
    return $raw.Split(';') | Where-Object { $_ -ne '' }
}

function Add-ToUserPath($dir) {
    $entries = Get-UserPathEntries
    if ($entries -contains $dir) {
        Write-Step "PATH already contains $dir"
        return $false
    }
    $updated = (@($entries) + $dir) -join ';'
    if ($updated.Length -gt 2000) {
        Write-Warn "Your user PATH is $($updated.Length) characters, which is close to the limit."
        Write-Warn "Skipping the PATH change. Run the exe by full path, or tidy your PATH first."
        return $false
    }
    [Environment]::SetEnvironmentVariable('Path', $updated, 'User')
    Write-Good "Added $dir to your user PATH"
    return $true
}

function Remove-FromUserPath($dir) {
    $entries = Get-UserPathEntries
    if ($entries -notcontains $dir) { return $false }
    $updated = ($entries | Where-Object { $_ -ne $dir }) -join ';'
    [Environment]::SetEnvironmentVariable('Path', $updated, 'User')
    Write-Good "Removed $dir from your user PATH"
    return $true
}

# --- uninstall ---------------------------------------------------------------

if ($Uninstall) {
    Write-Host ""
    Write-Host "Uninstalling $AppName" -ForegroundColor Cyan
    Write-Host ""

    if (Test-Path $InstallTo) {
        Remove-Item $InstallTo -Recurse -Force
        Write-Good "Removed $InstallTo"
    } else {
        Write-Step "Nothing installed at $InstallTo"
    }

    $pathChanged = Remove-FromUserPath $InstallTo

    if ($Purge) {
        if (Test-Path $DataDir) {
            Remove-Item $DataDir -Recurse -Force
            Write-Good "Removed saved history at $DataDir"
        }
    } elseif (Test-Path $DataDir) {
        Write-Step "Saved history kept at $DataDir (use -Purge to delete it)"
    }

    Write-Host ""
    if ($pathChanged) {
        Write-Good "Done. Open a new terminal for the PATH change to apply."
    } else {
        Write-Good "Done."
    }
    Write-Host ""
    exit 0
}

# --- install -----------------------------------------------------------------

Write-Host ""
Write-Host "Installing $AppName for $env:USERNAME" -ForegroundColor Cyan
Write-Host ""

# 1. Find or build the binary.
$built = Join-Path $SourceDir $ExeName
# Resolve a path to go.exe. Written without ?? or ?: so this runs on Windows
# PowerShell 5.1 as well as PowerShell 7.
$goExe = $null
$goCmd = Get-Command go -ErrorAction SilentlyContinue
if ($goCmd) {
    $goExe = $goCmd.Source
} elseif (Test-Path 'C:\Program Files\Go\bin\go.exe') {
    # Go was installed after this terminal opened, so it is not on PATH yet.
    $goExe = 'C:\Program Files\Go\bin\go.exe'
}

if ($goExe) {
    Write-Step "Building with $goExe"
    Push-Location $SourceDir
    try {
        & $goExe build -o $ExeName ./cmd/flighttrack
        if ($LASTEXITCODE -ne 0) { throw "go build failed with exit code $LASTEXITCODE" }
        Write-Good "Built $ExeName"
    } finally {
        Pop-Location
    }
} elseif (Test-Path $built) {
    Write-Warn "Go not found; installing the existing $ExeName without rebuilding."
} else {
    Write-Host ""
    Write-Host "  Go is not installed and there is no prebuilt $ExeName here." -ForegroundColor Red
    Write-Host "  Install Go, then run this script again:" -ForegroundColor Red
    Write-Host ""
    Write-Host "      winget install --id GoLang.Go -e"
    Write-Host ""
    exit 1
}

if (-not (Test-Path $built)) { throw "expected $built after building, but it is not there" }

# 2. Copy it into place.
if (-not (Test-Path $InstallTo)) {
    New-Item -ItemType Directory -Path $InstallTo -Force | Out-Null
}
Copy-Item $built (Join-Path $InstallTo $ExeName) -Force
Write-Good "Installed to $InstallTo"

# 3. Put it on PATH.
$pathChanged = $false
if (-not $NoPath) {
    $pathChanged = Add-ToUserPath $InstallTo
} else {
    Write-Step "Left PATH alone (-NoPath)"
}

# 4. Tell the user what to do next.
Write-Host ""
Write-Good "Done."
Write-Host ""
if ($pathChanged) {
    Write-Host "  Open a NEW terminal, then run:" -ForegroundColor Cyan
} else {
    Write-Host "  Run:" -ForegroundColor Cyan
}
Write-Host ""
Write-Host "      flighttrack"
Write-Host "      flighttrack -flight QF2 -from SYD -to LHR"
Write-Host "      flighttrack watch -flights QF2,CX251"
Write-Host "      flighttrack help"
Write-Host ""
Write-Host "  Optional, for a larger API quota (free account at opensky-network.org):"
Write-Host ""
Write-Host '      setx OPENSKY_CLIENT_ID "your-id"'
Write-Host '      setx OPENSKY_CLIENT_SECRET "your-secret"'
Write-Host ""
Write-Host "  To remove it later:  .\install.ps1 -Uninstall"
Write-Host ""
