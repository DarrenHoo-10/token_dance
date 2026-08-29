[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$Path,
    [Parameter(Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [string]$ExpectedThumbprint
)

$ErrorActionPreference = 'Stop'
if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
    throw "BLOCKED: artifact not found: $Path"
}

$signature = Get-AuthenticodeSignature -LiteralPath $Path
if ($signature.Status -ne [System.Management.Automation.SignatureStatus]::Valid) {
    throw "BLOCKED: Authenticode signature is not valid for '$Path' (status=$($signature.Status); message=$($signature.StatusMessage))"
}
if ($null -eq $signature.SignerCertificate) {
    throw "BLOCKED: Authenticode signer certificate evidence is missing for '$Path'"
}

$actual = $signature.SignerCertificate.Thumbprint.Replace(' ', '').ToUpperInvariant()
$expected = $ExpectedThumbprint.Replace(' ', '').ToUpperInvariant()
if ($actual -ne $expected) {
    throw "BLOCKED: signer thumbprint mismatch for '$Path' (expected=$expected actual=$actual)"
}
if ($null -eq $signature.TimeStamperCertificate) {
    throw "BLOCKED: trusted Authenticode timestamp evidence is missing for '$Path'"
}

[pscustomobject]@{
    Path = (Resolve-Path -LiteralPath $Path).Path
    Status = $signature.Status.ToString()
    Subject = $signature.SignerCertificate.Subject
    Thumbprint = $actual
    TimeStamper = if ($signature.TimeStamperCertificate) { $signature.TimeStamperCertificate.Subject } else { $null }
}
