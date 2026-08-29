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

    $RequiredProductionReferences = @{
        "internal/store/mysql/auth.go"        = @('GetUserByID', 'GetPendingEmailChallenge', 'UpdateEmailChallengeAttempt', 'ListSessionsByUser')
        "internal/store/mysql/privacy.go"     = @('GetPrivacyByUser', 'LockPrivacyVersion', 'GetPublishedProfileByHandle', 'GetDeletionRequestByOwner', 'LockDeletionRequestForCancel', 'HidePublicProfileForDeletion')
        "internal/store/mysql/device.go"      = @('ListInstallationsByUser', 'GetInstallationByOwner', 'CancelBindingChallengeByOwner')
        "internal/store/mysql/media.go"       = @('CreateUploadObject', 'GetUploadObjectByOwner', 'UpdateUploadObjectStatus')
        "internal/store/mysql/export.go"      = @('GetExportJobByOwner', 'ListExportJobsByUser', 'CompleteExportJob')
        "internal/store/mysql/leaderboard.go" = @('GetLatestPublishedSnapshot', 'GetLatestPublishedSnapshotByBoard', 'ListVisibleLeaderboardEntries', 'DeleteSnapshotEntries')
        "internal/store/mysql/ingest.go"      = @('GetIngestInstallationByID', 'LockIngestBatch', 'UpdateInstallationLastSeen')
        "internal/store/mysql/search.go"      = @('SearchPublicUsers', 'SearchPublicSkills')
    }
    foreach ($Entry in $RequiredProductionReferences.GetEnumerator()) {
        $Source = Get-Content $Entry.Key -Raw
        if ($Source -notmatch 'tokendance/internal/store/sqlcgen') {
            throw "sqlc production wiring regression: $($Entry.Key) does not import sqlcgen"
        }
        foreach ($Symbol in $Entry.Value) {
            if ($Source -notmatch "\.${Symbol}\(") {
                throw "sqlc production wiring regression: $($Entry.Key) does not call $Symbol"
            }
        }
    }

    $ProductionSources = @(Get-ChildItem "internal/store/mysql/*.go", "internal/worker/*.go" -File | Where-Object { $_.Name -notlike '*_test.go' })
    $ProductionText = ($ProductionSources | ForEach-Object { Get-Content $_.FullName -Raw }) -join "`n"
    foreach ($NamedQuery in $NamedQueries) {
        $Symbol = [regex]::Match($NamedQuery.Line, '^-- name: (\w+)').Groups[1].Value
        if ($ProductionText -notmatch "\.${Symbol}\(") {
            throw "sqlc dead query regression: generated symbol $Symbol has no production reference"
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
