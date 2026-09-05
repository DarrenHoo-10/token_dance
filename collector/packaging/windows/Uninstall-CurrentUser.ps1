[CmdletBinding()]
param(
    [ValidateSet('Preserve', 'Remove', 'Prompt')]
    [string]$Spool = 'Prompt'
)

$ErrorActionPreference = 'Stop'
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = [Security.Principal.WindowsPrincipal]::new($identity)
if ($principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'Per-user uninstall must be run from a non-elevated shell.'
}

$runKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'
Remove-ItemProperty -Path $runKey -Name 'TokenShow Collector' -ErrorAction SilentlyContinue

$installDir = Join-Path $env:LOCALAPPDATA 'TokenShow\Collector'
$stateDir = Join-Path $env:LOCALAPPDATA 'TokenShow\CollectorState'
$spoolDir = Join-Path $stateDir 'spool'
Remove-Item -LiteralPath $installDir -Recurse -Force -ErrorAction SilentlyContinue

$choice = $Spool
if ((Test-Path -LiteralPath $spoolDir) -and $choice -eq 'Prompt') {
    $answer = Read-Host 'Preserve encrypted spool for a future reinstall? [Y/n]'
    $choice = if ($answer -match '^(n|no)$') { 'Remove' } else { 'Preserve' }
}
if ($choice -eq 'Remove') {
    Remove-Item -LiteralPath $spoolDir -Recurse -Force -ErrorAction SilentlyContinue
}
if ((Test-Path -LiteralPath $stateDir) -and -not (Get-ChildItem -LiteralPath $stateDir -Force | Select-Object -First 1)) {
    Remove-Item -LiteralPath $stateDir -Force
}

Write-Output "Uninstalled current-user collector. Spool policy: $choice"
