[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$CollectorExe,
    [Parameter(Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [string]$ExpectedSignerThumbprint,
    [ValidateSet('Preserve', 'Reset', 'Prompt')]
    [string]$ExistingSpool = 'Prompt',
    [switch]$NoAutostart
)

$ErrorActionPreference = 'Stop'
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = [Security.Principal.WindowsPrincipal]::new($identity)
if ($principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'Per-user installation must be run from a non-elevated shell.'
}

$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
& (Join-Path $scriptRoot 'Verify-Authenticode.ps1') -Path $CollectorExe -ExpectedThumbprint $ExpectedSignerThumbprint | Out-Host

$installDir = Join-Path $env:LOCALAPPDATA 'TokenShow\Collector'
$stateDir = Join-Path $env:LOCALAPPDATA 'TokenShow\CollectorState'
$spoolDir = Join-Path $stateDir 'spool'
if (Test-Path -LiteralPath $spoolDir) {
    $choice = $ExistingSpool
    if ($choice -eq 'Prompt') {
        $answer = Read-Host 'Existing encrypted spool found. Preserve it? [Y/n]'
        $choice = if ($answer -match '^(n|no)$') { 'Reset' } else { 'Preserve' }
    }
    if ($choice -eq 'Reset') {
        Remove-Item -LiteralPath $spoolDir -Recurse -Force
    }
}

New-Item -ItemType Directory -Path $installDir -Force | Out-Null
$destination = Join-Path $installDir 'tokenshow-collector.exe'
Copy-Item -LiteralPath $CollectorExe -Destination $destination -Force
& (Join-Path $scriptRoot 'Verify-Authenticode.ps1') -Path $destination -ExpectedThumbprint $ExpectedSignerThumbprint | Out-Host

if (-not $NoAutostart) {
    $runKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'
    New-Item -Path $runKey -Force | Out-Null
    New-ItemProperty -Path $runKey -Name 'TokenShow Collector' -Value ('"{0}" --background' -f $destination) -PropertyType String -Force | Out-Null
}

Write-Output "Installed for current user: $destination"
Write-Output "Spool policy: $ExistingSpool"
