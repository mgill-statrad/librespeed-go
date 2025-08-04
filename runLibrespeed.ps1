param (
    [Parameter(Mandatory = $true)]
    [string]$Url,

    [Parameter(Mandatory = $true)]
    [string]$Username,

    [Parameter(Mandatory = $true)]
    [string]$PasswordSecretName,

    [Parameter(Mandatory = $true)]
    [string]$ApiToken,

    [Parameter(Mandatory = $false)]
    [string]$BaseUrl = "https://nucleushealth.secretservercloud.com/",

    [Parameter(Mandatory = $false)]
    [string]$LocalJson = ".\speedtest_servers.json",

    [Parameter(Mandatory = $false)]
    [int]$ServerId = 3
)

# Import secret functions from external script
. "$PSScriptRoot\getSecret.ps1"

try {
    Write-Host "Resolving secret name '$PasswordSecretName' to ID..."
    $SecretId = Get-SecretId -BaseUrl $BaseUrl -ApiToken $ApiToken -SearchString $PasswordSecretName

    Write-Host "Fetching secret details..."
    $Secret = Get-Secret -BaseUrl $BaseUrl -ApiToken $ApiToken -SecretId $SecretId

    $templateName = $Secret.secretTemplateName
    $password = $null

    switch ($templateName) {
        'Password' {
            $creds = Get-Username-Password -BaseUrl $BaseUrl -ApiToken $ApiToken -SecretId $SecretId
            $password = $creds.Password
        }
        'Pin' {
            foreach ($item in $Secret.items) {
                if ($item.fieldName -eq 'Pin Code') {
                    $password = $item.itemValue
                    break
                }
            }
        }
        default {
            throw "Unsupported secret template: $templateName"
        }
    }

    if (-not $password) {
        throw "Password could not be extracted from secret template '$templateName'."
    }

    $exePath = ".\librespeed.exe"
    $exeArgs = @(
        "--url", $Url,
        "--username", $Username,
        "--password", $password,
        "--local-json", $LocalJson,
        "--server-id", $ServerId
    )

    $quotedArgs = $exeArgs | ForEach-Object { "`"$_`"" }
    $fullCommand = "$exePath " + ($quotedArgs -join ' ')
    Write-Host "Executing: $fullCommand"

    Start-Process -FilePath $exePath -ArgumentList $exeArgs -NoNewWindow -Wait

} catch {
    Write-Error "Error: $_"
}
