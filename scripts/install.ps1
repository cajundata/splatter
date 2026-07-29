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

# SIG # Begin signature block
# MIIFpAYJKoZIhvcNAQcCoIIFlTCCBZECAQExDzANBglghkgBZQMEAgEFADB5Bgor
# BgEEAYI3AgEEoGswaTA0BgorBgEEAYI3AgEeMCYCAwEAAAQQH8w7YFlLCE63JNLG
# KX7zUQIBAAIBAAIBAAIBAAIBADAxMA0GCWCGSAFlAwQCAQUABCAbrbVk3KzT8WHr
# oVXVUI+sEmZzhjs5C7T+N2GHTSLXsqCCAxQwggMQMIIB+KADAgECAhAd0LeIYxtE
# kEYnBB5rGMTkMA0GCSqGSIb3DQEBCwUAMCAxHjAcBgNVBAMMFVdlbGRvbiBDb2Rl
# IFNpZ25hdHVyZTAeFw0yNjAzMjYxNjE3MzNaFw0zMTAzMjYxNjI3MzJaMCAxHjAc
# BgNVBAMMFVdlbGRvbiBDb2RlIFNpZ25hdHVyZTCCASIwDQYJKoZIhvcNAQEBBQAD
# ggEPADCCAQoCggEBAM9aJKb4kvqBx+/aip0IR832wAUEiPmxPA4A6U4XP1IkpQEa
# UmFyO0vUrz8s9XCrP6vgO82pxdYpmqyl1zK8hobr2gXaLwOxkMnTxIaYH9CZgwEh
# +0TzUJvtv985OgFCcN8dxE6EbkwUfn9FxOjLt0/974QPyJtRaGLnpS31GKlO8x6x
# jPaGV8mF3JS2qo8HpAVz5ufUnzzDtcLz02okKtk+QZrfzWciyNd99A/mgUrW/e91
# Qr7QQbgCAduuf19mHHlzo5NOmbJyJ2K3KeEfJQXGGuapMqcMmZLgqVvUwuYIK6D8
# +O3jLbbvRcc330BKnk/135fLU6P/4nUtM7730akCAwEAAaNGMEQwDgYDVR0PAQH/
# BAQDAgeAMBMGA1UdJQQMMAoGCCsGAQUFBwMDMB0GA1UdDgQWBBTLDCqyX0L6n2iG
# FINeRYy9xHBxSTANBgkqhkiG9w0BAQsFAAOCAQEAVZsVUWQu2963PXCLGh3Bf0FY
# RVdEsZrHputh48QzEKKFdVzmUTFXE+FIcZptLDDNrH0H/zKTOY3Odfs1duAnm4Tx
# hx1uLSvldb6qCsh2becImT2lQUhLFzDcBCjcq2agZS3N4CzPbEi/2c9apMvuvYtR
# 1Hob389q4nHeffheZFVH3PRzfaGapKfanmBSgqxQRB/LQuHlTMBpvz8YYrWz2pyH
# e0LuxpqcpofTIpG2WcHXO+fwEkqsluJ9ydy039jLiHnt9zrD0PWp3O+rhWrS2rpP
# jS7wJ2s6cKWSI/ebV4U0JgC4FRhRUpbsF5Hw8sK/jl7hv4kKAk9cBTJMhuVUeDGC
# AeYwggHiAgEBMDQwIDEeMBwGA1UEAwwVV2VsZG9uIENvZGUgU2lnbmF0dXJlAhAd
# 0LeIYxtEkEYnBB5rGMTkMA0GCWCGSAFlAwQCAQUAoIGEMBgGCisGAQQBgjcCAQwx
# CjAIoAKAAKECgAAwGQYJKoZIhvcNAQkDMQwGCisGAQQBgjcCAQQwHAYKKwYBBAGC
# NwIBCzEOMAwGCisGAQQBgjcCARUwLwYJKoZIhvcNAQkEMSIEID8mq0QkdTGZCcFP
# 3br1N+6pg7oYCzD/cV79/1ugUQZ7MA0GCSqGSIb3DQEBAQUABIIBAC1m9gh2hJvp
# J9/SyRL0lE+TlW/GPlF1XMAQG/E/SLaqBGeUhRo2dT2i9EGEwJwylmr/rqJ2VLOs
# GM0WoPjYepnbltkCp56pCrV1D3Lh7iSyXYonEEHt/TKmCxosWMdXGY2tDIvTuF+B
# OyqYu6QhoX5UTz3ET8MEa0Q8wiNXtaslbYeYJtkEmjbt2A0z/Ei+VPTQeQTF0/Ri
# kI4wp7ONzmSrAiArdFoR7Cyyh3bHsOGD2gZME3tHgVq3ch+33qefseCZoMhjPdMJ
# L6DYOEYPMbq3YEnqRvirUKx8hluYfQkPR2FizWe3WVZyfqnePHT3AlvpSFl2URJy
# rZcwTsc8owo=
# SIG # End signature block
