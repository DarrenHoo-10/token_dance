$ErrorActionPreference = 'Stop'
$desktopRoot = Split-Path -Parent $PSScriptRoot
function Assert-WindowsGuiSubsystem([string]$Path) {
    $bytes = [IO.File]::ReadAllBytes($Path)
    if ($bytes.Length -lt 64 -or $bytes[0] -ne 0x4D -or $bytes[1] -ne 0x5A) {
        throw 'BLOCKED: TokenDance.exe is not a Windows executable'
    }
    $offset = [BitConverter]::ToInt32($bytes, 60)
    if ($offset -lt 64 -or ($offset + 94) -gt $bytes.Length) {
        throw 'BLOCKED: Invalid PE header'
    }
    if ([Text.Encoding]::ASCII.GetString($bytes, $offset, 4) -ne "PE`0`0") {
        throw 'BLOCKED: Invalid PE header'
    }
    $machine = [BitConverter]::ToUInt16($bytes, $offset + 4)
    if ($machine -ne 0x8664) {
        throw 'BLOCKED: TokenDance.exe must be a Windows x64 executable'
    }
    $subsystem = [BitConverter]::ToUInt16($bytes, $offset + 92)
    if ($subsystem -ne 2) {
        throw 'BLOCKED: TokenDance.exe must use the Windows GUI subsystem'
    }
}
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
    Assert-WindowsGuiSubsystem $binary
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
