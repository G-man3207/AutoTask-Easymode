param(
    [Parameter(Mandatory = $true)]
    [string]$AppId,
    [ValidateRange(1, 23)]
    [int]$Hours = 8,
    [string]$PolicyName = ""
)

$ErrorActionPreference = "Stop"

function AzJson {
    param([string[]]$Arguments)
    $text = & az @Arguments --output json
    if ($LASTEXITCODE -ne 0) {
        throw "az $($Arguments -join ' ') failed"
    }
    if ([string]::IsNullOrWhiteSpace($text)) {
        return $null
    }
    return $text | ConvertFrom-Json
}

function AzTsv {
    param([string[]]$Arguments)
    $text = & az @Arguments --output tsv
    if ($LASTEXITCODE -ne 0) {
        throw "az $($Arguments -join ' ') failed"
    }
    return ($text | Out-String).Trim()
}

function Invoke-GraphJsonBody {
    param(
        [string]$Method,
        [string]$Url,
        [object]$Body
    )
    $path = Join-Path $env:TEMP ("atem-graph-body-" + [Guid]::NewGuid().ToString("n") + ".json")
    try {
        $json = $Body | ConvertTo-Json -Depth 10 -Compress
        Set-Content -LiteralPath $path -Value $json -Encoding UTF8
        return AzJson @("rest", "--method", $Method, "--url", $Url, "--headers", "Content-Type=application/json", "--body", "@$path")
    } finally {
        if (Test-Path -LiteralPath $path) {
            Remove-Item -LiteralPath $path -Force
        }
    }
}

if ([string]::IsNullOrWhiteSpace($PolicyName)) {
    $PolicyName = "atem-mcp-copilot-api-access-token-${Hours}h"
}

$duration = "{0:D2}:00:00" -f $Hours
$definition = "{""TokenLifetimePolicy"":{""Version"":1,""AccessTokenLifetime"":""$duration""}}"
$appObjectId = AzTsv @("ad", "app", "show", "--id", $AppId, "--query", "id")

$policies = AzJson @("rest", "--method", "GET", "--url", "https://graph.microsoft.com/v1.0/policies/tokenLifetimePolicies")
$policy = $policies.value | Where-Object { $_.displayName -eq $PolicyName } | Select-Object -First 1
if (-not $policy) {
    $policy = Invoke-GraphJsonBody `
        -Method "POST" `
        -Url "https://graph.microsoft.com/v1.0/policies/tokenLifetimePolicies" `
        -Body @{
            definition            = @($definition)
            displayName           = $PolicyName
            isOrganizationDefault = $false
        }
    Write-Host "Created token lifetime policy $($policy.id) ($duration)." -ForegroundColor Green
} else {
    Write-Host "Reusing token lifetime policy $($policy.id)." -ForegroundColor Cyan
}

$assigned = AzJson @("rest", "--method", "GET", "--url", "https://graph.microsoft.com/v1.0/applications/$appObjectId/tokenLifetimePolicies")
if (-not ($assigned.value | Where-Object { $_.id -eq $policy.id })) {
    Invoke-GraphJsonBody `
        -Method "POST" `
        -Url "https://graph.microsoft.com/v1.0/applications/$appObjectId/tokenLifetimePolicies/`$ref" `
        -Body @{
            "@odata.id" = "https://graph.microsoft.com/v1.0/policies/tokenLifetimePolicies/$($policy.id)"
        } | Out-Null
    Write-Host "Assigned policy to application object $appObjectId." -ForegroundColor Green
} else {
    Write-Host "Policy is already assigned to application object $appObjectId." -ForegroundColor Cyan
}

AzJson @("rest", "--method", "GET", "--url", "https://graph.microsoft.com/v1.0/applications/$appObjectId/tokenLifetimePolicies") |
    ConvertTo-Json -Depth 10
