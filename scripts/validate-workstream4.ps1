param(
    [switch]$SkipViewerBuild
)

$ErrorActionPreference = "Stop"

function Write-Step($message) {
    Write-Host "`n==> $message" -ForegroundColor Cyan
}

function Get-ValidationFiles {
    $roots = @("internal", "web/viewer/src", "docs/explanations")
    $extensions = @("*.go", "*.ts", "*.md")

    $files = @()
    foreach ($root in $roots) {
        if (-not (Test-Path $root)) {
            continue
        }

        foreach ($ext in $extensions) {
            $files += Get-ChildItem -Path $root -Recurse -File -Filter $ext
        }
    }

    return $files
}

function Assert-NoPatternHits($pattern, $label) {
    $results = @()
    foreach ($file in (Get-ValidationFiles)) {
        $lineNumber = 0
        foreach ($line in (Get-Content -Path $file.FullName)) {
            $lineNumber++
            if ([System.Text.RegularExpressions.Regex]::IsMatch($line, $pattern)) {
                $results += "{0}:{1}:{2}" -f $file.FullName, $lineNumber, $line.Trim()
            }
        }
    }

    if ($results.Count -gt 0) {
        Write-Host "Validation failed: $label" -ForegroundColor Red
        $results | ForEach-Object { Write-Host $_ }
        throw "Validation check failed: $label"
    }
}

Write-Step "Running observability-focused Go tests"
go test ./internal/server/metrics ./internal/server/router ./internal/server/transport

Write-Step "Validating /metrics scrapeability and histogram signal under induced load"
go test ./internal/server/metrics -run TestPrometheusHandler_ScrapeExposesMetricFamilies -count=1
go test ./internal/server/router -run TestFanoutFrame_RecordsRoutingHistogramUnderLoad -count=1

Write-Step "Checking errorCategory values are within policy"
Assert-NoPatternHits 'errorCategory"\s*,\s*"(?!auth|protocol|transport|timeout|backpressure|internal)[a-z_]+' "Go log errorCategory contains out-of-policy value"
Assert-NoPatternHits 'errorCategory\s*:\s*"(?!auth|protocol|transport|timeout|backpressure|internal)[a-z_]+' "Viewer log errorCategory contains out-of-policy value"

Write-Step "Checking canonical structured key names"
$requiredKeys = @('sessionId', 'role', 'frameId', 'packetType', 'queueDepth', 'framesDropped', 'errorCategory')
$validationFiles = Get-ValidationFiles
foreach ($key in $requiredKeys) {
    $found = $false
    foreach ($file in $validationFiles) {
        if (Select-String -Path $file.FullName -Pattern "\b$key\b" -Quiet) {
            $found = $true
            break
        }
    }

    if (-not $found) {
        throw "Missing canonical key in source scan: $key"
    }
}

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

Write-Step "Workstream 4 validation completed successfully"
