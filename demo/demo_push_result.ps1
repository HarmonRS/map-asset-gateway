param(
    [string]$ProjectCode = "default",
    [string]$ResultCode = "",
    [string]$ResultID = "",
    [string]$TargetSystemCode = "",
    [string]$CallbackUrl = "",
    [string]$StyleVersionID = "",
    [string]$RequestedBy = "demo-user",
    [bool]$WaitForJob = $true,
    [bool]$IncludeArtifacts = $true,
    [int]$TimeoutSec = 180
)

. (Join-Path $PSScriptRoot "common.ps1")

Import-RsDemoEnvironment

if (-not $ResultID -and -not $ResultCode) {
    throw "ResultID or ResultCode is required"
}

$result = if ($ResultID) {
    Invoke-RsApi -Method "GET" -Path ("/api/v1/results/{0}" -f $ResultID)
} else {
    Get-RsResultByCode -ProjectCode $ProjectCode -Code $ResultCode
}

if (-not $StyleVersionID) {
    if ($result.delivery -and $result.delivery.style_version -and $result.delivery.style_version.id) {
        $StyleVersionID = $result.delivery.style_version.id
    } elseif ($result.styles -and $result.styles.Count -gt 0) {
        $StyleVersionID = $result.styles[0].id
    }
}
if (-not $StyleVersionID) {
    throw "No style version is bound to the target result"
}

$body = @{
    requested_by      = $RequestedBy
    style_version_id  = $StyleVersionID
    include_artifacts = $IncludeArtifacts
}
if ($TargetSystemCode) {
    $body.target_system_code = $TargetSystemCode
}
if ($CallbackUrl) {
    $body.callback_url = $CallbackUrl
}
if (-not $TargetSystemCode -and -not $CallbackUrl) {
    $body.callback_url = "http://127.0.0.1:8920/callback"
}

$push = Invoke-RsApi -Method "POST" -Path ("/api/v1/results/{0}/pushes" -f $result.id) -Body $body

$job = $null
if ($WaitForJob) {
    $job = Wait-RsJob -JobID $push.job.id -TimeoutSec $TimeoutSec
}

[ordered]@{
    api_base_url       = Get-RsApiBaseUrl
    result_id          = $result.id
    result_code        = $result.code
    style_version_id   = $StyleVersionID
    target_system_code = $push.target_system_code
    callback_url       = $push.callback_url
    publish_id         = $push.publish_id
    job_id             = $push.job.id
    job_status         = if ($job) { $job.status } else { $push.job.status }
    resource_kind      = $push.resource_kind
} | ConvertTo-Json -Depth 8
