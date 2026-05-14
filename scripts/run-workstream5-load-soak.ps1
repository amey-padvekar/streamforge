param(
    [ValidateSet(2, 5, 10)]
    [int[]]$ViewerCounts = @(2, 5, 10),
    [ValidateRange(30, 60)]
    [int]$SoakMinutes = 30,
    [string]$ResultsFile = "test/integration/stability-results.jsonl",
    [switch]$SkipSoak
)

$ErrorActionPreference = "Stop"

function Write-Step($message) {
    Write-Host "`n==> $message" -ForegroundColor Cyan
}

$repoRoot = Split-Path -Parent $PSScriptRoot
Push-Location $repoRoot
try {
    $viewerArg = ($ViewerCounts -join ",")
    $soakDuration = "{0}m" -f $SoakMinutes

    $resultsPathAbs = if ([System.IO.Path]::IsPathRooted($ResultsFile)) {
        $ResultsFile
    } else {
        Join-Path $repoRoot $ResultsFile
    }
    $resultsDir = Split-Path -Parent $resultsPathAbs
    if (-not (Test-Path $resultsDir)) {
        New-Item -Path $resultsDir -ItemType Directory -Force | Out-Null
    }

    if (Test-Path $resultsPathAbs) {
        Remove-Item $resultsPathAbs -Force
    }

    $env:STREAMFORGE_STABILITY_RESULTS = $resultsPathAbs

    Write-Step "Running load scenarios: multiple viewers, slow viewer, reconnect storm"
    $env:STREAMFORGE_MULTI_VIEWER_COUNTS = $viewerArg
    go test ./test/integration -run "TestStability_MultiViewerMatrix|TestStability_SlowViewerSimulation|TestStability_ReconnectStormSimulation" -count=1 -v -timeout 30m

    if (-not $SkipSoak) {
        Write-Step "Running soak scenario for $soakDuration"
        $env:STREAMFORGE_ENABLE_SOAK = "1"
        $env:STREAMFORGE_SOAK_DURATION = $soakDuration
        go test ./test/integration -run TestStability_Soak30To60Minutes -count=1 -v -timeout 90m
    }

    Write-Step "Stability scenario summary"
    if (-not (Test-Path $resultsPathAbs)) {
        throw "Expected results file not found: $resultsPathAbs"
    }

    $rows = Get-Content $resultsPathAbs | Where-Object { $_.Trim() -ne "" } | ForEach-Object { $_ | ConvertFrom-Json }
    foreach ($row in $rows) {
        $scenario = $row.scenario
        $p50 = [math]::Round([double]$row.latencyP50Ms, 2)
        $p95 = [math]::Round([double]$row.latencyP95Ms, 2)
        $dropRatePct = [math]::Round(([double]$row.dropRate * 100.0), 3)
        $framesSent = $row.framesSent
        $framesDropped = $row.framesDropped
        $memoryGrowth = if ($null -ne $row.memoryGrowthMiB) { [math]::Round([double]$row.memoryGrowthMiB, 3) } else { $null }

        if ($null -ne $memoryGrowth) {
            Write-Host ("- {0}: p50={1}ms p95={2}ms dropRate={3}% dropped={4}/{5} memoryGrowth={6}MiB" -f $scenario, $p50, $p95, $dropRatePct, $framesDropped, $framesSent, $memoryGrowth)
        } else {
            Write-Host ("- {0}: p50={1}ms p95={2}ms dropRate={3}% dropped={4}/{5}" -f $scenario, $p50, $p95, $dropRatePct, $framesDropped, $framesSent)
        }
    }

    Write-Step "Completed Workstream 5.4 scenario runs"
    Write-Host "Results file: $resultsPathAbs"
}
finally {
    Remove-Item Env:STREAMFORGE_ENABLE_SOAK -ErrorAction SilentlyContinue
    Remove-Item Env:STREAMFORGE_SOAK_DURATION -ErrorAction SilentlyContinue
    Remove-Item Env:STREAMFORGE_MULTI_VIEWER_COUNTS -ErrorAction SilentlyContinue
    Remove-Item Env:STREAMFORGE_STABILITY_RESULTS -ErrorAction SilentlyContinue
    Pop-Location
}
