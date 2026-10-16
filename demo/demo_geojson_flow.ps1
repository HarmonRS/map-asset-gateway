param(
    [string]$ProjectCode = "default",
    [string]$ResourceCode = ("demo_geojson_" + (Get-Date -Format "yyyyMMdd_HHmmss")),
    [string]$InputPath = "",
    [string]$RequestedBy = "demo-user",
    [switch]$Push,
    [string]$TargetSystemCode = "",
    [string]$CallbackUrl = "",
    [bool]$WaitForJob = $true,
    [switch]$StartStack,
    [switch]$StartCallbackReceiver,
    [switch]$KeepProcesses,
    [int]$ApiPort = 28910,
    [int]$TilePort = 28900,
    [int]$CallbackPort = 28920
)

. (Join-Path $PSScriptRoot "common.ps1")

$stack = $null
$callback = $null
$run = New-RsRunContext -Prefix "geojson_flow"

try {
    Import-RsDemoEnvironment

    if ($StartStack) {
        $stack = Start-RsDemoStack -ApiPort $ApiPort -TilePort $TilePort -IncludeWorker -RunID $run.RunID
    }
    if ($StartCallbackReceiver) {
        $callback = Start-RsCallbackReceiver -Port $CallbackPort -RunID ($run.RunID + "_callback")
        $Push = $true
        $CallbackUrl = $callback.CallbackURL
    }

    if (-not $InputPath) {
        $geojsonDir = Join-Path (Get-RsRepoRoot) "data\geojson\demo"
        New-Item -ItemType Directory -Force -Path $geojsonDir | Out-Null
        $InputPath = Join-Path $geojsonDir ($ResourceCode + ".geojson")
        @"
{
  "type": "FeatureCollection",
  "features": [
    {
      "type": "Feature",
      "properties": {
        "code": "$ResourceCode",
        "kind": "demo"
      },
      "geometry": {
        "type": "Polygon",
        "coordinates": [
          [
            [110.0, 20.0],
            [112.0, 20.0],
            [112.0, 22.0],
            [110.0, 22.0],
            [110.0, 20.0]
          ]
        ]
      }
    }
  ]
}
"@ | Set-Content -LiteralPath $InputPath -Encoding UTF8
    }

    $publicUrl = "/geojson/demo/" + [System.IO.Path]::GetFileName($InputPath)
    $project = Encode-RsURIComponent $ProjectCode
    $resource = Invoke-RsApi -Method "POST" -Path "/api/v1/projects/$project/geojson-resources" -Body @{
        code        = $ResourceCode
        name        = $ResourceCode
        description = "Registered by demo_geojson_flow.ps1"
        storage_uri = ConvertTo-RsFileUri $InputPath
        public_url  = $publicUrl
        metadata    = @{
            demo_run = $run.RunID
        }
    }

    $pushResponse = $null
    $job = $null
    if ($Push -or $TargetSystemCode -or $CallbackUrl) {
        $body = @{
            resource_kind       = "geojson_resource"
            geojson_resource_id = $resource.id
            requested_by        = $RequestedBy
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

        $pushResponse = Invoke-RsApi -Method "POST" -Path "/api/v1/projects/$project/delivery-pushes" -Body $body
        if ($WaitForJob) {
            $job = Wait-RsJob -JobID $pushResponse.job.id
        }
    }

    $callbackRecord = $null
    if ($callback) {
        Start-Sleep -Seconds 1
        $callbackRecord = Get-RsLatestCallbackRecord -OutputDirectory $callback.OutputDirectory
    }

    [ordered]@{
        run_id               = $run.RunID
        api_base_url         = Get-RsApiBaseUrl
        tile_base_url        = Get-RsTileBaseUrl
        resource_id          = $resource.id
        resource_code        = $resource.code
        input_path           = [System.IO.Path]::GetFullPath($InputPath)
        public_url           = $resource.public_url
        download_url         = $resource.download_url
        push_job_id          = if ($pushResponse) { $pushResponse.job.id } else { $null }
        push_job_status      = if ($job) { $job.status } elseif ($pushResponse) { $pushResponse.job.status } else { $null }
        callback_url         = if ($pushResponse) { $pushResponse.callback_url } else { $CallbackUrl }
        callback_record_path = if ($callbackRecord) { $callbackRecord.Path } else { $null }
    } | ConvertTo-Json -Depth 12
}
finally {
    if (-not $KeepProcesses) {
        if ($callback) {
            Stop-RsBackgroundProcesses -Processes @($callback.Process)
        }
        if ($stack) {
            Stop-RsBackgroundProcesses -Processes $stack.Processes
        }
    }
}
