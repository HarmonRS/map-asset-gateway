param(
    [string]$ProjectCode = "default",
    [string]$ResultCode = ("demo_full_" + (Get-Date -Format "yyyyMMdd_HHmmss")),
    [string]$ResultType = "ndvi",
    [string]$SourceSystemCode = "",
    [string]$RequestedBy = "demo-user",
    [string]$StyleCode = "demo_continuous_style",
    [string]$TargetSystemCode = "demo_callback",
    [switch]$StartStack,
    [switch]$StartCallbackReceiver,
    [switch]$KeepProcesses,
    [int]$ApiPort = 28910,
    [int]$TilePort = 28900,
    [int]$CallbackPort = 28920,
    [int]$ZoomMin = 0,
    [int]$ZoomMax = 3
)

. (Join-Path $PSScriptRoot "common.ps1")

$stack = $null
$callback = $null
$run = New-RsRunContext -Prefix "full_flow"

try {
    Import-RsDemoEnvironment

    if ($StartStack) {
        $stack = Start-RsDemoStack -ApiPort $ApiPort -TilePort $TilePort -IncludeWorker -RunID $run.RunID
    }

    $callbackUrl = ""
    if ($StartCallbackReceiver) {
        $callback = Start-RsCallbackReceiver -Port $CallbackPort -RunID ($run.RunID + "_callback")
        $callbackUrl = $callback.CallbackURL
    }

    $inputPath = Join-Path $run.OutputDirectory ($ResultCode + ".tif")
    & (Get-RsPythonExe) (Join-Path $PSScriptRoot "create_sample_raster.py") `
        --output $inputPath `
        --preset "ndvi" | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to create sample raster"
    }

    $inspection = Invoke-RsApi -Method "POST" -Path "/api/v1/admin/raster-inspections" -Body @{
        path = [System.IO.Path]::GetFullPath($inputPath)
    }

    $styleVersion = Ensure-RsStyleVersion -ProjectCode $ProjectCode -StyleCode $StyleCode -StyleName "Demo Continuous Style"

    $project = Encode-RsURIComponent $ProjectCode
    $importBody = @{
        requested_by       = $RequestedBy
        asset              = @{
            source_ref   = $inspection.storage_uri
            asset_type   = "raster"
            display_name = $ResultCode
            description  = "Imported by demo_full_flow.ps1"
            metadata     = @{
                demo_run = $run.RunID
            }
        }
        asset_version      = @{
            format       = $inspection.format
            storage_uri  = $inspection.storage_uri
            mime_type    = $inspection.mime_type
            size_bytes   = $inspection.size_bytes
            crs          = $inspection.crs
            width        = $inspection.width
            height       = $inspection.height
            band_count   = $inspection.band_count
            dtype        = $inspection.dtype
            nodata_value = $inspection.nodata_value
            bbox         = $inspection.bbox
            resolution_x = $inspection.resolution_x
            resolution_y = $inspection.resolution_y
            metadata     = $inspection.metadata
        }
        product            = @{
            code        = $ResultCode
            type        = $ResultType
            name        = $ResultCode
            description = "Demo end-to-end result"
        }
        product_version    = @{
            value_type = "continuous"
            unit       = "index"
            bbox       = $inspection.bbox
            status     = "ready"
        }
        style_binding      = @{
            style_version_id = $styleVersion.id
            is_default       = $true
            availability     = "enabled"
        }
    }
    if ($SourceSystemCode) {
        $importBody.source_system_code = $SourceSystemCode
    }

    $import = Invoke-RsApi -Method "POST" -Path "/api/v1/projects/$project/imports/results" -Body $importBody

    $slice = Invoke-RsApi -Method "POST" -Path ("/api/v1/results/{0}/slice" -f $import.product.product.id) -Body @{
        requested_by     = $RequestedBy
        build_cog        = $true
        generate_preview = $true
        generate_legend  = $true
        generate_tiles   = $true
        build_cog_input  = @{
            compression   = "deflate"
            block_size    = 256
            force_rebuild = $true
        }
        preview_input    = @{
            style_version_id = $styleVersion.id
            width            = 512
            height           = 512
            format           = "png"
            force_rebuild    = $true
        }
        legend_input     = @{
            style_version_id = $styleVersion.id
            width            = 320
            height           = 80
            format           = "png"
            force_rebuild    = $true
        }
        tiles_input      = @{
            style_version_id = $styleVersion.id
            zoom_min         = $ZoomMin
            zoom_max         = $ZoomMax
            tile_size        = 256
            tile_format      = "webp"
            force_rebuild    = $true
        }
    }

    $sliceJobs = @()
    foreach ($job in @($slice.jobs)) {
        $sliceJobs += Wait-RsJob -JobID $job.id
    }

    if ($callbackUrl) {
        Ensure-RsTargetSystem `
            -ProjectCode $ProjectCode `
            -Code $TargetSystemCode `
            -Name "Demo Callback Receiver" `
            -CallbackUrl $callbackUrl `
            -CallbackMethod "POST" `
            -TimeoutSeconds 30 `
            -CreatedBy $RequestedBy `
            -CallbackHeaders @{
                "X-Demo-Mode" = "full-flow"
            } | Out-Null
    }

    $pushBody = @{
        requested_by      = $RequestedBy
        style_version_id  = $styleVersion.id
        include_artifacts = $true
    }
    if ($callbackUrl) {
        $pushBody.target_system_code = $TargetSystemCode
    } else {
        $pushBody.callback_url = "http://127.0.0.1:8920/callback"
    }

    $push = Invoke-RsApi -Method "POST" -Path ("/api/v1/results/{0}/pushes" -f $import.product.product.id) -Body $pushBody
    $pushJob = Wait-RsJob -JobID $push.job.id

    $delivery = Invoke-RsApi -Method "GET" -Path ("/api/v1/projects/{0}/products/{1}/delivery?version=latest&style_version_id={2}" -f `
        (Encode-RsURIComponent $ProjectCode), `
        (Encode-RsURIComponent $ResultCode), `
        (Encode-RsURIComponent $styleVersion.id))

    $callbackRecord = $null
    if ($callback) {
        Start-Sleep -Seconds 1
        $callbackRecord = Get-RsLatestCallbackRecord -OutputDirectory $callback.OutputDirectory
    }

    [ordered]@{
        run_id               = $run.RunID
        api_base_url         = Get-RsApiBaseUrl
        tile_base_url        = Get-RsTileBaseUrl
        callback_url         = if ($callback) { $callback.CallbackURL } else { $push.callback_url }
        result_id            = $import.product.product.id
        result_code          = $ResultCode
        product_version_id   = $import.product_version.id
        style_version_id     = $styleVersion.id
        slice_job_ids        = @($sliceJobs | ForEach-Object { $_.id })
        push_job_id          = $push.job.id
        push_job_status      = $pushJob.status
        cog_url              = $delivery.downloads.cog_url
        preview_url          = if ($delivery.preview) { $delivery.preview.url } else { $null }
        legend_url           = if ($delivery.legend) { $delivery.legend.url } else { $null }
        tileset_url          = if ($delivery.tileset) { $delivery.tileset.url_template } else { $null }
        callback_record_path = if ($callbackRecord) { $callbackRecord.Path } else { $null }
        run_directory        = $run.RunDirectory
        log_directory        = if ($stack) { $stack.LogDirectory } else { $null }
    } | ConvertTo-Json -Depth 16
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
