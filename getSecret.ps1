<#
.SYNOPSIS
    Retrieve secrets from Thycotic Secret Server (SecretServerCloud) via REST API.

.DESCRIPTION
    This script provides functions to search for secrets, retrieve secret details, and extract username/password or pin code
    from secrets stored in Thycotic Secret Server. It can be run interactively or dot-sourced and called from other scripts.
    The script supports passing parameters directly or via prompts for interactive use.

.PARAMETER SecretSearchString
    (Optional) The search string to find the secret by name or metadata.

.PARAMETER BaseUrl
    (Optional) The base URL of the Secret Server instance. Defaults to "https://nucleushealth.secretservercloud.com/".

.PARAMETER ApiTokenParam
    (Optional) The API token for authenticating to Secret Server. If not provided, the script will prompt for it.

.FUNCTIONS
    Get-SecretId           - Searches for a secret and returns its ID.
    Get-Secret             - Retrieves the full secret object by ID.
    Get-Username-Password  - Extracts username and password from a secret.

.EXAMPLE
    # Run interactively and follow prompts
    .\getSecret.ps1

.EXAMPLE
    # Dot-source and call from another script
    . .\getSecret.ps1
    $secretId = Get-SecretId -BaseUrl $BaseUrl -ApiToken $ApiToken -SearchString "my_secret"
    $creds = Get-Username-Password -BaseUrl $BaseUrl -ApiToken $ApiToken -SecretId $secretId

.NOTES
    - Requires a valid API token with appropriate permissions.
    - Designed for use with Thycotic Secret Server REST API v1.
    - Can be used as a standalone script or as a library of functions.

#>

param (
    [Parameter(Mandatory = $false)]
    [string]$SecretSearchString,

    [Parameter(Mandatory = $false)]
    [string]$BaseUrl = "https://nucleushealth.secretservercloud.com/",

    [Parameter(Mandatory = $false)]
    [string]$ApiTokenParam
)

function Get-SecretId {
    <#
    .SYNOPSIS
        Searches for a secret in Thycotic Secret Server and returns its ID.

    .DESCRIPTION
        Uses the REST API to search for a secret by name or metadata. Returns the ID of the first matching secret.
        If multiple secrets are found, displays their names and IDs and returns the first match.

    .PARAMETER BaseUrl
        The base URL of the Secret Server instance.

    .PARAMETER ApiToken
        The API token for authenticating to Secret Server.

    .PARAMETER SearchString
        The search string to find the secret by name or metadata.

    .OUTPUTS
        [string] The ID of the first matching secret.

    .EXAMPLE
        $secretId = Get-SecretId -BaseUrl $BaseUrl -ApiToken $ApiToken -SearchString "my_secret"
    #>
    param (
        [string]$BaseUrl,
        [string]$ApiToken,
        [string]$SearchString
    )

    if (-not $ApiToken) {
        throw "ApiToken is required but was not provided to Get-SecretId."
    }

    $searchUrl = "$BaseUrl/api/v1/secrets?filter.searchText=$([uri]::EscapeDataString($SearchString))"
    $headers = @{
        Authorization = "Bearer $ApiToken"
    }
    Write-Host "Search URL: $searchUrl"
    $response = Invoke-RestMethod -Method Get -Uri $searchUrl -Headers $headers
    if ($response.records.Count -eq 0) {
        throw "No secrets found matching search string '$SearchString'."
    } elseif ($response.records.Count -gt 1) {
        Write-Host "Multiple secrets found. Using the first match:"
        foreach ($secret in $response.records) {
            Write-Host "  Name: $($secret.name), ID: $($secret.id)"
        }
    }
    return $response.records[0].id
}

function Get-Secret {
    <#
    .SYNOPSIS
        Retrieves the full secret object from Thycotic Secret Server by secret ID.

    .DESCRIPTION
        Uses the REST API to fetch all details of a secret given its ID.

    .PARAMETER BaseUrl
        The base URL of the Secret Server instance.

    .PARAMETER ApiToken
        The API token for authenticating to Secret Server.

    .PARAMETER SecretId
        The ID of the secret to retrieve.

    .OUTPUTS
        [PSCustomObject] The secret object returned by the API.

    .EXAMPLE
        $secret = Get-Secret -BaseUrl $BaseUrl -ApiToken $ApiToken -SecretId $secretId
    #>
    param (
        [string]$BaseUrl,
        [string]$ApiToken,
        [string]$SecretId
    )

    $secretUrl = "$BaseUrl/api/v1/secrets/$SecretId"
    $headers = @{
        Authorization = "Bearer $ApiToken"
    }
    $response = Invoke-RestMethod -Method Get -Uri $secretUrl -Headers $headers
    return $response
}

function Get-Username-Password {
    <#
    .SYNOPSIS
        Extracts username and password from a secret in Thycotic Secret Server.

    .DESCRIPTION
        Retrieves the secret by ID and extracts the username and password fields from the secret items.

    .PARAMETER BaseUrl
        The base URL of the Secret Server instance.

    .PARAMETER ApiToken
        The API token for authenticating to Secret Server.

    .PARAMETER SecretId
        The ID of the secret to retrieve.

    .OUTPUTS
        [PSCustomObject] An object with Username and Password properties.

    .EXAMPLE
        $creds = Get-Username-Password -BaseUrl $BaseUrl -ApiToken $ApiToken -SecretId $secretId
        $creds.Username
        $creds.Password
    #>
    param (
        [string]$BaseUrl,
        [string]$ApiToken,
        [string]$SecretId
    )

    $secretUrl = "$BaseUrl/api/v1/secrets/$SecretId"
    $headers = @{
        Authorization = "Bearer $ApiToken"
    }
    $response = Invoke-RestMethod -Method Get -Uri $secretUrl -Headers $headers

    $username = $null
    $password = $null

    if ($response.items) {
        foreach ($item in $response.items) {
            if ($item.fieldName -eq 'Username') {
                $username = $item.itemValue
            }
            elseif ($item.fieldName -eq 'Password') {
                $password = $item.itemValue
            }
        }
    }

    return [PSCustomObject]@{
        Username = $username
        Password = $password
    }
}

# Only execute main logic if not being dot-sourced or imported as a module
if ($MyInvocation.InvocationName -eq '.') {
    return
}

# Prompt for missing parameters if running interactively
if (-not $SecretSearchString) {
    $SecretSearchString = Read-Host "Enter Secret Search String"
}
if (-not $BaseUrl) {
    $BaseUrl = Read-Host "Enter BaseUrl (e.g., https://your-secretserver.com/SecretServer)"
}
if (-not $ApiTokenParam) {
    $ApiTokenParam = Read-Host "Enter API Token"
}

try {
    Write-Host "Searching for secret matching '$SecretSearchString'..."
    $SecretId = Get-SecretId -BaseUrl $BaseUrl -ApiToken $ApiTokenParam -SearchString $SecretSearchString

    Write-Host "Fetching secret details for secret ID $SecretId..."
    $secret = Get-Secret -BaseUrl $BaseUrl -ApiToken $ApiTokenParam -SecretId $SecretId
    Write-Debug "Secret object:`n$($secret | Format-List | Out-String)"

    $templateName = $secret.secretTemplateName

    if ($templateName -eq 'Password') {
        Write-Host "Secret template is 'Password'. Fetching username and password..."
        $creds = Get-Username-Password -BaseUrl $BaseUrl -ApiToken $ApiTokenParam -SecretId $SecretId
        Write-Output "Username: $($creds.Username)"
        Write-Output "Password: $($creds.Password)"
    }
    elseif ($templateName -eq 'Pin') {
        Write-Host "Secret template is 'Pin'. Fetching Pin Code..."
        $pinCode = $null
        if ($secret.items) {
            foreach ($item in $secret.items) {
                if ($item.fieldName -eq 'Pin Code') {
                    $pinCode = $item.itemValue
                    break
                }
            }
        }
        if ($pinCode) {
            Write-Output "Pin Code: $pinCode"
        } else {
            Write-Output "Pin Code not found in secret items."
        }
    }
    else {
        Write-Output "Unknown secret template: $templateName"
    }
}
catch {
    Write-Error "An error occurred: $_"
}