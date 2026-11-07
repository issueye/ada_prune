# Builds AdaPrune for Windows.
#
#   powershell -ExecutionPolicy Bypass -File scripts/build-windows.ps1
#   powershell -ExecutionPolicy Bypass -File scripts/build-windows.ps1 -Plain
#
# Produces:
#   build/windows-amd64/AdaPrune.exe        desktop app (GUI subsystem, no console)
#   build/windows-amd64/prune-server.exe      headless service (console)
#
# The desktop app must not open a console window, so it is built with
# -H=windowsgui and a manifest, and the script reads the PE header afterwards to
# prove the subsystem is GUI (2), not console (3).
param(
    [string]$Arch = "amd64",
    [switch]$Plain,   # skip the mygo CLI; plain go build with -H=windowsgui
    [string]$Version = ""
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Set-Location $root

if (-not $Version) {
    $Version = (Get-Content (Join-Path $root "mygo.json") -Raw | ConvertFrom-Json).version
}
$commit = "dev"
try {
    $candidate = (git rev-parse --short HEAD 2>$null)
    if ($candidate) { $commit = $candidate }
} catch { }

$module = "github.com/learningdog1/ada_prune"
$ldbase = "-s -w -X $module/internal/version.Version=$Version -X $module/internal/version.Commit=$commit"

Write-Host "Building AdaPrune $Version ($commit) for windows/$Arch" -ForegroundColor Cyan

$out = Join-Path $root "build/windows-$Arch"
New-Item -ItemType Directory -Force $out | Out-Null

# Desktop app first: the mygo CLI owns build/windows-<arch> and clears it, so the
# headless binary must be written after it, not before.
if ($Plain) {
    Write-Host "  AdaPrune.exe (plain build)" -ForegroundColor DarkGray
    & go build -trimpath -tags mygo_noinspector -ldflags "$ldbase -H=windowsgui" `
        -o (Join-Path $out "AdaPrune.exe") ./cmd/prune-desktop
    if ($LASTEXITCODE -ne 0) { throw "go build prune-desktop failed ($LASTEXITCODE)" }
} else {
    Write-Host "  AdaPrune.exe (mygo build: icon, manifest, version)" -ForegroundColor DarkGray
    # The mygo CLI reads mygo.json for the name, the icon and the entry point.
    # Not $args: PowerShell reserves that name for the script arguments.
    $mygoArgs = @("tool", "mygo", "build", "-platform", "windows/$Arch", "-skip-installer")
    & go @mygoArgs
    if ($LASTEXITCODE -ne 0) { throw "mygo build failed ($LASTEXITCODE)" }
    New-Item -ItemType Directory -Force $out | Out-Null
}

# Headless service: a console program, which is what a server wants.
Write-Host "  prune-server.exe" -ForegroundColor DarkGray
& go build -trimpath -ldflags $ldbase -o (Join-Path $out "prune-server.exe") ./cmd/prune-server
if ($LASTEXITCODE -ne 0) { throw "go build prune-server failed ($LASTEXITCODE)" }

# Prove the desktop binary is a GUI program: subsystem 2, not 3.
$exe = Join-Path $out "AdaPrune.exe"
if (-not (Test-Path $exe)) {
    # The mygo CLI names its output from mygo.json; fall back to any new exe.
    $exe = Get-ChildItem (Join-Path $root "build") -Recurse -Filter "*.exe" |
        Where-Object { $_.Name -notmatch "setup|Setup|prune-server" } |
        Sort-Object LastWriteTime -Descending |
        Select-Object -First 1 -ExpandProperty FullName
}
if (-not $exe -or -not (Test-Path $exe)) { throw "no desktop executable was produced under build/" }

$bytes = [System.IO.File]::ReadAllBytes($exe)
$peOffset = [BitConverter]::ToInt32($bytes, 0x3C)
$optional = $peOffset + 24
$subsystem = [BitConverter]::ToUInt16($bytes, $optional + 68)
$kind = switch ($subsystem) {
    2 { "GUI (no console)" }
    3 { "console (a console window will open)" }
    default { "unknown ($subsystem)" }
}

Write-Host ""
Write-Host ("  {0}" -f $exe.Replace("$root\", "")) -ForegroundColor Green
Write-Host ("  {0:N1} MB - subsystem: {1}" -f ((Get-Item $exe).Length / 1MB), $kind)
$server = Join-Path $out "prune-server.exe"
if (Test-Path $server) {
    Write-Host ("  {0}" -f $server.Replace("$root\", "")) -ForegroundColor Green
    Write-Host ("  {0:N1} MB - headless" -f ((Get-Item $server).Length / 1MB))
}
if ($subsystem -eq 3) { throw "the desktop executable still opens a console window" }
