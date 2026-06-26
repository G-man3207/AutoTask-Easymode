# Azure deployment

This repo deploys the hosted Microsoft 365 Copilot-facing MCP server to Azure
Container Apps.

Do not commit tenant-specific IDs, resource names, FQDNs, Autotask credentials,
or profile JSON here. Keep live values in Azure, GitHub repository
secrets/variables, Key Vault, or a private runbook.

## Resources

The bootstrap script creates or reuses:

- Azure Container Registry
- Azure Key Vault
- Azure Container Apps environment
- Azure Container App
- Entra app registration/service principal for GitHub OIDC
- Federated credential scoped to `repo:<owner>/<repo>:ref:refs/heads/main`
- GitHub repository secrets and variables used by the deploy workflow

The Container App exposes:

```text
https://<container-app-fqdn>/healthz
https://<container-app-fqdn>/mcp
```

## GitOps flow

`.github/workflows/deploy-azure-containerapp.yml` runs on push to `main`:

1. Build, vet, lint, and race-enabled tests.
2. Login to Azure through GitHub OIDC.
3. Build the Docker image with commit/build metadata.
4. Push tags to ACR:
   - `atem-mcp:${GITHUB_SHA}`
   - `atem-mcp:latest`
5. Update the Container App image.

Repository secrets set by bootstrap:

- `AZURE_CLIENT_ID`
- `AZURE_TENANT_ID`
- `AZURE_SUBSCRIPTION_ID`

Repository variables set by bootstrap:

- `AZURE_RESOURCE_GROUP`
- `AZURE_LOCATION`
- `AZURE_ACR_NAME`
- `AZURE_CONTAINER_APP_NAME`
- `AZURE_CONTAINER_APP_ENVIRONMENT`
- `AZURE_KEY_VAULT_NAME`

## Bootstrap

Run this once from an authenticated Azure CLI + GitHub CLI session:

```pwsh
./scripts/azure/bootstrap.ps1 `
  -SubscriptionId "<subscription-id>" `
  -ResourceGroup "<resource-group>" `
  -Location "<azure-region>" `
  -AcrName "<globally-unique-acr-name>" `
  -KeyVaultName "<globally-unique-key-vault-name>" `
  -GitHubRepo "<owner>/<repo>"
```

The script is intentionally idempotent: it reuses existing resources when they
already exist. It also builds the first image with `az acr build`, so local Docker
is not required.

## Key Vault secrets

Before production-style testing, store the Autotask and profile secrets in Key
Vault:

```pwsh
az keyvault secret set --vault-name "<key-vault-name>" --name atem-username --value "<autotask-api-username>"
az keyvault secret set --vault-name "<key-vault-name>" --name atem-secret --value "<autotask-api-secret>"
az keyvault secret set --vault-name "<key-vault-name>" --name atem-integration-code --value "<autotask-integration-code>"
az keyvault secret set --vault-name "<key-vault-name>" --name atem-auth-profiles --value '<profile-json-array>'
```

Example `atem-auth-profiles` value:

```json
[
  {
    "tenantId": "<tenant-id>",
    "objectId": "<test-user-object-id>",
    "resourceId": 29682903,
    "roleId": 29683464,
    "scopes": ["company:read", "ticket:read", "ticket:create", "time:add", "report:read"]
  }
]
```

Then attach the Key Vault secrets to the Container App:

```pwsh
$kv = "https://<key-vault-name>.vault.azure.net/secrets"

az containerapp secret set -g "<resource-group>" -n "<container-app-name>" --secrets `
  atem-username=keyvaultref:$kv/atem-username,identityref:system `
  atem-secret=keyvaultref:$kv/atem-secret,identityref:system `
  atem-code=keyvaultref:$kv/atem-integration-code,identityref:system `
  atem-profiles=keyvaultref:$kv/atem-auth-profiles,identityref:system

az containerapp update -g "<resource-group>" -n "<container-app-name>" --set-env-vars `
  ATEM_AUTH_MODE=entra `
  ATEM_ENTRA_TENANT_ID="<tenant-id>" `
  ATEM_ENTRA_AUDIENCE="<copilot-api-app-audience>" `
  ATEM_USERNAME=secretref:atem-username `
  ATEM_SECRET=secretref:atem-secret `
  ATEM_INTEGRATION_CODE=secretref:atem-code `
  ATEM_AUTH_PROFILES=secretref:atem-profiles `
  ATEM_QUEUE_ID="<queue-id>" `
  ATEM_TICKET_STATUS_NEW="<new-status-id>" `
  ATEM_TICKET_STATUS_COMPLETE="<complete-status-id>"
```

The Container App can start with `ATEM_AUTH_MODE=none` for smoke testing before
the Microsoft 365 Copilot app registration/audience exists. Switch it to `entra`
before connecting a real Copilot test user.

## Copilot OAuth connection lifetime

Copilot Studio's MCP OAuth configuration should request a refresh token:

```text
openid profile offline_access api://<api-app-client-id>/access_as_user
```

If Copilot Studio still marks the MCP connection stale after the access token
expires, use an app-scoped Microsoft Entra token lifetime policy as a pragmatic
workaround. This does not change refresh-token lifetimes and does not apply to
the whole tenant; it extends access/ID token lifetime for the ATEM MCP API app.

```pwsh
./scripts/azure/set-token-lifetime.ps1 `
  -AppId "<api-app-client-id>" `
  -Hours 8
```

The script uses Microsoft Graph through `az rest`, creates or reuses a policy
named `atem-mcp-copilot-api-access-token-<hours>h`, and assigns it to the app
registration.
