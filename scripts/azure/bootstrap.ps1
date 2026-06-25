param(
    [Parameter(Mandatory = $true)]
    [string]$SubscriptionId,
    [string]$ResourceGroup = "autotask-easymode",
    [string]$Location = "swedencentral",
    [string]$ContainerAppName = "atem-mcp",
    [string]$ContainerAppEnvironment = "cae-atem-dev",
    [Parameter(Mandatory = $true)]
    [string]$AcrName,
    [Parameter(Mandatory = $true)]
    [string]$KeyVaultName,
    [string]$ImageName = "atem-mcp",
    [Parameter(Mandatory = $true)]
    [string]$GitHubRepo,
    [string]$GitHubAppDisplayName = "github-atem-deploy",
    [switch]$SkipGitHub,
    [switch]$SkipInitialImage
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

function AzExists {
    param([string[]]$Arguments)
    try {
        & az @Arguments --only-show-errors --output none *> $null
        return $LASTEXITCODE -eq 0
    } catch {
        return $false
    }
}

function Ensure-ResourceGroup {
    if (-not (AzExists @("group", "show", "--name", $ResourceGroup))) {
        & az group create --name $ResourceGroup --location $Location --output none
    }
}

function Ensure-Acr {
    if (-not (AzExists @("acr", "show", "--resource-group", $ResourceGroup, "--name", $AcrName))) {
        & az acr create `
            --resource-group $ResourceGroup `
            --name $AcrName `
            --location $Location `
            --sku Basic `
            --admin-enabled false `
            --output none
    }
}

function Ensure-KeyVault {
    if (-not (AzExists @("keyvault", "show", "--resource-group", $ResourceGroup, "--name", $KeyVaultName))) {
        & az keyvault create `
            --resource-group $ResourceGroup `
            --name $KeyVaultName `
            --location $Location `
            --enable-rbac-authorization true `
            --output none
    }
}

function Ensure-ContainerAppEnvironment {
    if (-not (AzExists @("containerapp", "env", "show", "--resource-group", $ResourceGroup, "--name", $ContainerAppEnvironment))) {
        & az containerapp env create `
            --resource-group $ResourceGroup `
            --name $ContainerAppEnvironment `
            --location $Location `
            --output none
    }
}

function Ensure-RoleAssignment {
    param(
        [string]$PrincipalId,
        [string]$Role,
        [string]$Scope
    )
    $existing = AzTsv @("role", "assignment", "list", "--assignee", $PrincipalId, "--role", $Role, "--scope", $Scope, "--query", "[0].id")
    if ([string]::IsNullOrWhiteSpace($existing)) {
        & az role assignment create `
            --assignee-object-id $PrincipalId `
            --assignee-principal-type ServicePrincipal `
            --role $Role `
            --scope $Scope `
            --output none
    }
}

function Ensure-GitHubDeployApp {
    $appId = AzTsv @("ad", "app", "list", "--display-name", $GitHubAppDisplayName, "--query", "[0].appId")
    if ([string]::IsNullOrWhiteSpace($appId)) {
        $appId = AzTsv @("ad", "app", "create", "--display-name", $GitHubAppDisplayName, "--query", "appId")
    }

    $spObjectId = AzTsv @("ad", "sp", "list", "--filter", "appId eq '$appId'", "--query", "[0].id")
    if ([string]::IsNullOrWhiteSpace($spObjectId)) {
        $spObjectId = AzTsv @("ad", "sp", "create", "--id", $appId, "--query", "id")
    }

    $subject = "repo:$($GitHubRepo):ref:refs/heads/main"
    $credName = "github-main"
    $existingCred = AzTsv @("ad", "app", "federated-credential", "list", "--id", $appId, "--query", "[?name=='$credName'].name | [0]")
    if ([string]::IsNullOrWhiteSpace($existingCred)) {
        $cred = @{
            name = $credName
            issuer = "https://token.actions.githubusercontent.com"
            subject = $subject
            description = "GitHub Actions deploy from main"
            audiences = @("api://AzureADTokenExchange")
        }
        $credPath = Join-Path $env:TEMP "atem-github-federated-credential.json"
        $cred | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $credPath -Encoding UTF8
        & az ad app federated-credential create --id $appId --parameters $credPath --output none
        Remove-Item -LiteralPath $credPath -Force
    }

    $rgScope = "/subscriptions/$SubscriptionId/resourceGroups/$ResourceGroup"
    $acr = AzJson @("acr", "show", "--resource-group", $ResourceGroup, "--name", $AcrName)
    Ensure-RoleAssignment -PrincipalId $spObjectId -Role "Contributor" -Scope $rgScope
    Ensure-RoleAssignment -PrincipalId $spObjectId -Role "AcrPush" -Scope $acr.id

    return [pscustomobject]@{
        AppId = $appId
        ServicePrincipalObjectId = $spObjectId
    }
}

function Set-GitHubConfig {
    param([string]$ClientId)
    gh secret set AZURE_CLIENT_ID --repo $GitHubRepo --body $ClientId
    gh secret set AZURE_TENANT_ID --repo $GitHubRepo --body (AzTsv @("account", "show", "--query", "tenantId"))
    gh secret set AZURE_SUBSCRIPTION_ID --repo $GitHubRepo --body $SubscriptionId

    gh variable set AZURE_RESOURCE_GROUP --repo $GitHubRepo --body $ResourceGroup
    gh variable set AZURE_LOCATION --repo $GitHubRepo --body $Location
    gh variable set AZURE_ACR_NAME --repo $GitHubRepo --body $AcrName
    gh variable set AZURE_CONTAINER_APP_NAME --repo $GitHubRepo --body $ContainerAppName
    gh variable set AZURE_CONTAINER_APP_ENVIRONMENT --repo $GitHubRepo --body $ContainerAppEnvironment
    gh variable set AZURE_KEY_VAULT_NAME --repo $GitHubRepo --body $KeyVaultName
}

function Build-InitialImage {
    $commit = AzTsv @("account", "show", "--query", "id")
    if ((Test-Path .git) -and (Get-Command git -ErrorAction SilentlyContinue)) {
        $gitExe = "git"
        $repoCommit = & $gitExe rev-parse HEAD 2>$null
        if ($LASTEXITCODE -eq 0 -and -not [string]::IsNullOrWhiteSpace($repoCommit)) {
            $commit = $repoCommit.Trim()
        }
    }
    $buildTime = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
    & az acr build `
        --registry $AcrName `
        --image "$($ImageName):bootstrap" `
        --image "$($ImageName):latest" `
        --build-arg "ATEM_COMMIT=$commit" `
        --build-arg "ATEM_BUILD_TIME=$buildTime" `
        .
}

function Ensure-ContainerApp {
    $acrLoginServer = AzTsv @("acr", "show", "--resource-group", $ResourceGroup, "--name", $AcrName, "--query", "loginServer")
    $image = "$acrLoginServer/$($ImageName):latest"
    if (-not (AzExists @("containerapp", "show", "--resource-group", $ResourceGroup, "--name", $ContainerAppName))) {
        & az containerapp create `
            --resource-group $ResourceGroup `
            --name $ContainerAppName `
            --environment $ContainerAppEnvironment `
            --image $image `
            --ingress external `
            --target-port 8080 `
            --transport http `
            --registry-server $acrLoginServer `
            --registry-identity system `
            --system-assigned `
            --min-replicas 1 `
            --max-replicas 3 `
            --cpu 0.25 `
            --memory 0.5Gi `
            --env-vars ATEM_HTTP_ADDR=:8080 ATEM_MCP_TOOLSET=m365 `
            --output none
    }

    $app = AzJson @("containerapp", "show", "--resource-group", $ResourceGroup, "--name", $ContainerAppName)
    $kv = AzJson @("keyvault", "show", "--resource-group", $ResourceGroup, "--name", $KeyVaultName)
    Ensure-RoleAssignment -PrincipalId $app.identity.principalId -Role "Key Vault Secrets User" -Scope $kv.id
    return $app
}

az account set --subscription $SubscriptionId

Ensure-ResourceGroup
Ensure-Acr
Ensure-KeyVault
Ensure-ContainerAppEnvironment

if (-not $SkipInitialImage) {
    Build-InitialImage
}

$app = Ensure-ContainerApp
$githubApp = Ensure-GitHubDeployApp

if (-not $SkipGitHub) {
    Set-GitHubConfig -ClientId $githubApp.AppId
}

$fqdn = AzTsv @("containerapp", "show", "--resource-group", $ResourceGroup, "--name", $ContainerAppName, "--query", "properties.configuration.ingress.fqdn")

Write-Host "Bootstrap complete."
Write-Host "Container App: https://$fqdn"
Write-Host "ACR: $AcrName"
Write-Host "Key Vault: $KeyVaultName"
Write-Host "GitHub deploy app client id: $($githubApp.AppId)"
