$ErrorActionPreference = 'Stop'
$desktopRoot = Split-Path -Parent $PSScriptRoot
function Get-Sha256([string]$Path) {
    $stream = [System.IO.File]::OpenRead($Path)
    $algorithm = [System.Security.Cryptography.SHA256]::Create()
    try { return [BitConverter]::ToString($algorithm.ComputeHash($stream)).Replace('-', '') }
    finally { $stream.Dispose(); $algorithm.Dispose() }
}
Push-Location $desktopRoot
try {
    $sourceJson = & node (Join-Path $PSScriptRoot 'build-source.mjs')
    if ($LASTEXITCODE -ne 0) { throw 'BLOCKED: release source verification failed' }
    $source = $sourceJson | ConvertFrom-Json
    if ($source.branch -ne 'main' -or $source.commitSha -notmatch '^[a-f0-9]{40}$' -or $source.dirty -ne $false) {
        throw 'BLOCKED: release source metadata must identify a clean main checkout and full commit SHA'
    }
    & npm.cmd run build
    if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed' }
    & cargo build --locked --release --manifest-path src-tauri/Cargo.toml --features custom-protocol
    if ($LASTEXITCODE -ne 0) { throw 'Native build failed' }

    $releaseDir = Join-Path $desktopRoot 'release'
    New-Item -ItemType Directory -Path $releaseDir -Force | Out-Null
    $binary = Join-Path $releaseDir 'TokenDance.exe'
    Copy-Item -LiteralPath (Join-Path $desktopRoot 'src-tauri/target/release/tokendance-desktop.exe') -Destination $binary -Force
    $manifest = [ordered]@{
        branch = $source.branch
        commitSha = $source.commitSha
        dirty = $source.dirty
        profile = 'release'
        builtAt = (Get-Date).ToUniversalTime().ToString('o')
        executable = 'TokenDance.exe'
        sha256 = Get-Sha256 $binary
        frontendSha256 = Get-Sha256 (Join-Path $desktopRoot 'dist/index.html')
        embeddedFrontend = $true
    }
    $manifest | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $releaseDir 'build-info.json') -Encoding UTF8
    Write-Output "Windows release ready: $binary"
} finally {
    Pop-Location
}
