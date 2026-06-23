#!/usr/bin/env pwsh
# Build and install the single atem binary into the Go bin directory (the one on
# PATH), stamping it with the current git commit and build time. This is the one
# source of truth for the runnable binary: source lives in the repo, and this
# script turns that source into the exe that `atem` resolves to on PATH.
#
# NOTE: the Go module is named `autotask-easymode`, so a bare `go install .`
# produces `autotask-easymode.exe`, NOT `atem.exe` -- which is why a hand-built
# `atem.exe` on PATH silently went stale (go install never touched it). We build
# with an explicit `-o .../atem.exe` so the binary on PATH is always the one that
# gets rebuilt, and we remove any stray autotask-easymode.exe to keep one binary.
#
# `atem version` then reports the commit + build time, so a stale binary (built
# from older source) is immediately distinguishable from a fresh one.
#
# Usage:
#   ./scripts/install.ps1        build current source and install to <GOPATH>/bin

$ErrorActionPreference = 'Stop'

# Make sure go is reachable even in a shell opened before Go was installed.
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    $env:Path = [System.Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' +
                [System.Environment]::GetEnvironmentVariable('Path', 'User')
}
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Host 'go is not installed or not on PATH.' -ForegroundColor Red
    exit 1
}

$repo = Split-Path -Parent $PSScriptRoot

# Resolve a git.exe. On this machine git ships with GitHub Desktop rather than
# being on PATH, so fall back to its bundled copy. Commit metadata is best-effort:
# if git can't be found the build still succeeds, stamped "unknown".
function Resolve-Git {
    $g = Get-Command git -ErrorAction SilentlyContinue
    if ($g) { return $g.Source }
    $candidates = @(
        "$env:LOCALAPPDATA\GitHubDesktop\app-*\resources\app\git\cmd\git.exe",
        "$env:ProgramFiles\Git\cmd\git.exe",
        "${env:ProgramFiles(x86)}\Git\cmd\git.exe"
    )
    foreach ($c in $candidates) {
        $found = Get-ChildItem $c -ErrorAction SilentlyContinue | Select-Object -Last 1
        if ($found) { return $found.FullName }
    }
    return $null
}

$commit = 'unknown'
$git = Resolve-Git
if ($git) {
    try {
        $rev = (& $git -C $repo rev-parse --short HEAD).Trim()
        if ($LASTEXITCODE -eq 0 -and $rev) {
            $commit = $rev
            $dirty = & $git -C $repo status --porcelain
            if ($dirty) { $commit = "$commit-dirty" }
        }
    } catch {
        # leave $commit = 'unknown'
    }
} else {
    Write-Host 'git not found - stamping commit as "unknown".' -ForegroundColor Yellow
}

$buildTime = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
$ldflags = "-X main.commit=$commit -X main.buildTime=$buildTime"

# Install location: GOBIN if set, else <GOPATH>/bin (both are conventionally on PATH).
$gobin = (go env GOBIN).Trim()
if (-not $gobin) { $gobin = Join-Path (go env GOPATH).Trim() 'bin' }
$bin = Join-Path $gobin 'atem.exe'

Write-Host "=== installing atem (commit $commit, built $buildTime) ===" -ForegroundColor Cyan
Push-Location $repo
try {
    go build -ldflags $ldflags -o $bin .
    if ($LASTEXITCODE -ne 0) {
        Write-Host 'FAILED: go build' -ForegroundColor Red
        exit 1
    }
} finally {
    Pop-Location
}

# Remove the stray binary a bare `go install .` would have produced, so there is
# exactly one atem on PATH.
$stray = Join-Path $gobin 'autotask-easymode.exe'
if (Test-Path $stray) {
    Remove-Item $stray -Force
    Write-Host "Removed stray binary: $stray" -ForegroundColor Yellow
}

Write-Host "Installed: $bin" -ForegroundColor Green
& $bin version
