param(
    [string]$ProjectCode = "default",
    [string]$Code = "demo_callback",
    [string]$Name = "Demo Callback Receiver",
    [string]$CallbackUrl = "http://127.0.0.1:8920/callback",
    [string]$CallbackMethod = "POST",
    [int]$TimeoutSeconds = 30,
    [string]$CreatedBy = "demo-user"
)

. (Join-Path $PSScriptRoot "common.ps1")

Import-RsDemoEnvironment

$target = Ensure-RsTargetSystem `
    -ProjectCode $ProjectCode `
    -Code $Code `
    -Name $Name `
    -CallbackUrl $CallbackUrl `
    -CallbackMethod $CallbackMethod `
    -TimeoutSeconds $TimeoutSeconds `
    -CreatedBy $CreatedBy `
    -CallbackHeaders @{
        "X-Demo-System" = "tile-server-demo"
    }

$target | ConvertTo-Json -Depth 8
