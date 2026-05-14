param(
    [string]$ResultsFile = "test/integration/stability-results.jsonl",
    [double]$MaxSoakP95Ms = 25,
    [double]$MaxSoakMemoryGrowthMiB = 16,
    [double]$MaxSoakDropRate = 0.99,
    [switch]$RunSoak,
    [ValidateRange(30, 60)]
    [int]$SoakMinutes = 30,
    [switch]$SkipViewerBuild
)

$ErrorActionPreference = "Stop"

function Write-Step($message) {
    Write-Host "`n==> $message" -ForegroundColor Cyan
}

$repoRoot = Split-Path -Parent $PSScriptRoot
Push-Location $repoRoot
try {
    $resultsPathAbs = if ([System.IO.Path]::IsPathRooted($ResultsFile)) {
        $ResultsFile
    } else {
        Join-Path $repoRoot $ResultsFile
    }

    Write-Step "Running protocol, session, and stability tests"
    go test ./internal/protocol ./internal/server/session ./test/integration -count=1 -v -timeout 30m

    if ($RunSoak) {
        Write-Step "Running soak scenario"
        $env:STREAMFORGE_ENABLE_SOAK = "1"
        $env:STREAMFORGE_SOAK_DURATION = "{0}m" -f $SoakMinutes
        $env:STREAMFORGE_STABILITY_RESULTS = $resultsPathAbs
        try {
            go test ./test/integration -run TestStability_Soak30To60Minutes -count=1 -v -timeout 90m
        }
        finally {
            Remove-Item Env:STREAMFORGE_ENABLE_SOAK -ErrorAction SilentlyContinue
            Remove-Item Env:STREAMFORGE_SOAK_DURATION -ErrorAction SilentlyContinue
            Remove-Item Env:STREAMFORGE_STABILITY_RESULTS -ErrorAction SilentlyContinue
        }
    }

    if (-not (Test-Path $resultsPathAbs)) {
        throw "Stability results file not found: $resultsPathAbs"
    }

    Write-Step "Validating soak metrics"
    $entries = Get-Content $resultsPathAbs | Where-Object { $_.Trim() -ne "" } | ForEach-Object { $_ | ConvertFrom-Json }
    $soak = $entries | Where-Object { $_.scenario -eq "soak" } | Select-Object -Last 1

    if ($null -eq $soak) {
        throw "No soak summary found in $resultsPathAbs"
    }

    if ($soak.latencyP95Ms -gt $MaxSoakP95Ms) {
        throw "Soak p95 latency too high: $($soak.latencyP95Ms)ms > $MaxSoakP95Ms ms"
    }

    if ($soak.memoryGrowthMiB -gt $MaxSoakMemoryGrowthMiB) {
        throw "Soak memory growth too high: $($soak.memoryGrowthMiB)MiB > $MaxSoakMemoryGrowthMiB MiB"
    }

    if ($soak.dropRate -gt $MaxSoakDropRate) {
        throw "Soak drop rate too high: $($soak.dropRate) > $MaxSoakDropRate"
    }

    $summary = [ordered]@{
        resultsFile          = $resultsPathAbs
        soakP95Ms            = $soak.latencyP95Ms
        soakMemoryGrowthMiB  = $soak.memoryGrowthMiB
        soakDropRate         = $soak.dropRate
        maxSoakP95Ms         = $MaxSoakP95Ms
        maxSoakMemoryGrowthMiB = $MaxSoakMemoryGrowthMiB
        maxSoakDropRate      = $MaxSoakDropRate
    }

    Write-Host ("validation_summary={0}" -f ($summary | ConvertTo-Json -Compress))

    if (-not $SkipViewerBuild) {
        Write-Step "Building viewer"
        Push-Location web/viewer
        try {
            npm run build
        }
        finally {
            Pop-Location
        }
    }

    Write-Step "Workstream 5 validation completed successfully"
}
finally {
    Pop-Location
}