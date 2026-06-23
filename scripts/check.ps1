#!/usr/bin/env pwsh
# Local quality gate for atem (Windows / PowerShell). Mirrors CI:
#   build -> vet -> lint (incl. gofumpt formatting) -> tests + coverage.
#
# Usage:
#   ./scripts/check.ps1          run the full gate
#   ./scripts/check.ps1 -Fix     auto-apply gofumpt formatting first
param(
    [switch]$Fix
)

$ErrorActionPreference = 'Stop'
$GolangciVersion = 'v2.12.2'

# Make sure go is reachable even in a shell opened before Go was installed.
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    $env:Path = [System.Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' +
                [System.Environment]::GetEnvironmentVariable('Path', 'User')
}
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Host 'go is not installed or not on PATH.' -ForegroundColor Red
    exit 1
}

$gopath = (go env GOPATH).Trim()
$gcl = Join-Path $gopath 'bin\golangci-lint.exe'
$cov = Join-Path (Get-Location) 'coverage.out'
if (-not (Test-Path $gcl)) {
    Write-Host "Installing golangci-lint $GolangciVersion ..." -ForegroundColor Yellow
    go install "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$GolangciVersion"
}

function Invoke-Step {
    param([string]$Name, [scriptblock]$Block)
    Write-Host "=== $Name ===" -ForegroundColor Cyan
    & $Block
    if ($LASTEXITCODE -ne 0) {
        Write-Host "FAILED: $Name" -ForegroundColor Red
        exit 1
    }
}

if ($Fix) {
    Invoke-Step 'format (apply gofumpt)' { & $gcl fmt }
}

Invoke-Step 'build' { go build ./... }
Invoke-Step 'vet' { go vet ./... }
# `golangci-lint run` enforces both the strict linters and gofumpt formatting.
Invoke-Step 'lint' { & $gcl run }

# The race detector needs a C toolchain (cgo). Use it when gcc is available;
# otherwise run plain tests locally — CI always runs -race on Linux.
# -count=1 forces a fresh run so the merged coverage profile is always written
# (cached packages don't emit one); an absolute path avoids ./... ambiguity.
$hasCC = (Get-Command gcc -ErrorAction SilentlyContinue) -and ($env:CGO_ENABLED -ne '0')
if ($hasCC) {
    Invoke-Step 'test (race + coverage)' {
        go test ./... -race -count=1 -covermode=atomic -coverprofile="$cov"
    }
} else {
    Write-Host 'gcc not found - running tests without -race (CI runs -race on Linux).' -ForegroundColor Yellow
    Invoke-Step 'test (coverage)' {
        go test ./... -count=1 -covermode=atomic -coverprofile="$cov"
    }
}

Invoke-Step 'coverage summary' { go tool cover "-func=$cov" | Select-Object -Last 1 }

Write-Host 'All checks passed.' -ForegroundColor Green
