# Build splatter from this clone and put it on the user PATH (Windows, PS7).
# Run from the repo root: .\scripts\install.ps1
# First run creates %LOCALAPPDATA%\splatter\bin and registers it on the user
# PATH; later runs just rebuild the exe in place. Requires Go on PATH.
$ErrorActionPreference = 'Stop'

if (-not (Test-Path '.\cmd\splatter')) {
    throw 'run this from the splatter repo root (cmd\splatter not found)'
}

$version = 'dev'
try {
    $described = & git describe --tags --always --dirty 2>&1
    if ($LASTEXITCODE -eq 0) { $version = "$described" }
} catch { }

$dest = Join-Path $env:LOCALAPPDATA 'splatter\bin'
New-Item -ItemType Directory -Force -Path $dest | Out-Null
$exe = Join-Path $dest 'splatter.exe'

& go build -ldflags "-X main.version=$version" -o $exe .\cmd\splatter
if ($LASTEXITCODE -ne 0) { throw 'go build failed' }

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($userPath -split ';') -notcontains $dest) {
    [Environment]::SetEnvironmentVariable('Path', "$userPath;$dest", 'User')
    Write-Output "added $dest to user PATH - open a new shell to pick it up"
}
Write-Output "installed splatter $version to $exe"
