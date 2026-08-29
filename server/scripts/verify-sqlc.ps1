$ErrorActionPreference = "Stop"

$SqlcVersion = "v1.30.0"
$ServerRoot = Split-Path -Parent $PSScriptRoot
Push-Location $ServerRoot
try {
    $Sqlc = Get-Command sqlc -ErrorAction SilentlyContinue
    $InstalledVersion = if ($Sqlc) { (& $Sqlc.Source version).Trim() } else { "" }
    if ($Sqlc -and $InstalledVersion -eq $SqlcVersion) {
        & $Sqlc.Source generate
    } else {
        go run "github.com/sqlc-dev/sqlc/cmd/sqlc@$SqlcVersion" generate
    }
    if ($LASTEXITCODE -ne 0) {
        throw "sqlc generation failed"
    }

    git diff --exit-code -- internal/store/sqlcgen
    if ($LASTEXITCODE -ne 0) {
        throw "sqlc generated files are out of date"
    }

    $Untracked = @(git ls-files --others --exclude-standard -- internal/store/sqlcgen)
    if ($LASTEXITCODE -ne 0) {
        throw "failed to inspect generated files"
    }
    if ($Untracked.Count -ne 0) {
        throw "sqlc generation produced untracked files"
    }
} finally {
    Pop-Location
}
