$ErrorActionPreference = "Stop"

$SqlcVersion = "v1.30.0"
$ServerRoot = Split-Path -Parent $PSScriptRoot
Push-Location $ServerRoot
try {
    $GeneratedRoot = Join-Path $ServerRoot "internal/store/sqlcgen"
    $GeneratedBefore = @(Get-ChildItem $GeneratedRoot -File | Sort-Object Name | ForEach-Object {
        "$($_.Name):$((Get-FileHash $_.FullName -Algorithm SHA256).Hash)"
    })

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

    $NamedQueries = @(Select-String -Path "db/queries/*.sql" -Pattern '^-- name: ')
    if ($NamedQueries.Count -lt 25) {
        throw "sqlc coverage regression: expected at least 25 named queries, found $($NamedQueries.Count)"
    }

    $RequiredStaticAreas = @('EmailChallenge', 'Privacy', 'Installation', 'UploadObject', 'ExportJob', 'Leaderboard', 'Ingest', 'Deletion')
    foreach ($Area in $RequiredStaticAreas) {
        if (-not ($NamedQueries.Line -match $Area)) {
            throw "sqlc coverage regression: missing static $Area queries"
        }
    }

    $DynamicReviews = @(Select-String -Path "internal/store/mysql/*.go" -Pattern 'sqlc-dynamic-reviewed:')
    if ($DynamicReviews.Count -lt 2) {
        throw "dynamic SQL review markers are missing"
    }

    $GeneratedAfter = @(Get-ChildItem $GeneratedRoot -File | Sort-Object Name | ForEach-Object {
        "$($_.Name):$((Get-FileHash $_.FullName -Algorithm SHA256).Hash)"
    })
    if (Compare-Object $GeneratedBefore $GeneratedAfter) {
        throw "sqlc generated files are out of date"
    }
} finally {
    Pop-Location
}
