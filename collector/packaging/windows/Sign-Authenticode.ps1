[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$Path,
    [string]$CertificateThumbprint = $env:WINDOWS_SIGNING_CERT_THUMBPRINT,
    [string]$TimestampUrl = 'http://timestamp.digicert.com'
)

$ErrorActionPreference = 'Stop'
if (-not $CertificateThumbprint) {
    throw 'BLOCKED: WINDOWS_SIGNING_CERT_THUMBPRINT certificate evidence is missing'
}
if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
    throw "BLOCKED: artifact not found: $Path"
}
$signtool = (Get-Command signtool.exe -ErrorAction SilentlyContinue).Source
if (-not $signtool) {
    throw 'BLOCKED: signtool.exe is unavailable'
}
$thumbprint = $CertificateThumbprint.Replace(' ', '').ToUpperInvariant()
$certificate = Get-ChildItem Cert:\CurrentUser\My | Where-Object { $_.Thumbprint -eq $thumbprint } | Select-Object -First 1
if (-not $certificate) {
    throw "BLOCKED: Authenticode certificate is not installed for current user: $thumbprint"
}

& $signtool sign /sha1 $thumbprint /fd SHA256 /tr $TimestampUrl /td SHA256 $Path
if ($LASTEXITCODE -ne 0) { throw "signtool failed with exit code $LASTEXITCODE" }
& (Join-Path (Split-Path -Parent $MyInvocation.MyCommand.Path) 'Verify-Authenticode.ps1') -Path $Path -ExpectedThumbprint $thumbprint
