param(
    [string]$ProjectCode = "default",
    [string]$ResultCode = ("demo_result_" + (Get-Date -Format "yyyyMMdd_HHmmss")),
    [string]$ResultType = "ndvi",
    [string]$InputPath = "",
    [string]$SourceSystemCode = "",
    [string]$RequestedBy = "demo-user",
    [string]$StyleCode = "demo_continuous_style",
    [string]$StyleName = "Demo Continuous Style",
    [int]$ZoomMin = 0,
    [int]$ZoomMax = 3,
    [bool]$WaitForJobs = $true,
    [switch]$StartStack,
    [switch]$KeepStack,
    [int]$ApiPort = 28910,
    [int]$TilePort = 28900
)

. (Join-Path $PSScriptRoot "common.ps1")

$stack = $null
$run = New-RsRunContext -Prefix "import_slice"

try {
    Import-RsDemoEnvironment

    if ($StartStack) {
        $stack = Start-RsDemoStack -ApiPort $ApiPort -TilePort $TilePort -IncludeWorker -RunID $run.RunID
    }

    if (-not $InputPath) {
        $InputPath = Join-Path $run.OutputDirectory ($ResultCode + ".tif")
        & (Get-RsPythonExe) (Join-Path $PSScriptRoot "create_sample_raster.py") `
            --output $InputPath `
            --preset "ndvi" | Out-Null
        if ($LASTEXITCODE -ne 0) {
            throw "Failed to create sample raster"
        }
    }

    $inspection = Invoke-RsApi -Method "POST" -Path "/api/v1/admin/raster-inspections" -Body @{
        path = [System.IO.Path]::GetFullPath($InputPath)
    }

    $styleVersion = Ensure-RsStyleVersion -ProjectCode $ProjectCode -StyleCode $StyleCode -StyleName $StyleName

    $assetVersionBody = @{
        format      = $inspection.format
        storage_uri = $inspection.storage_uri
        mime_type   = $inspection.mime_type
        size_bytes  = $inspection.size_bytes
        crs         = $inspection.crs
        width       = $inspection.width
        height      = $inspection.height
        band_count  = $inspection.band_count
        dtype       = $inspection.dtype
        bbox        = $inspection.bbox
        metadata    = $inspection.metadata
    }
    if ($inspection.PSObject.Properties.Name -contains "nodata_value" -and $null -ne $inspection.nodata_value) {
        $assetVersionBody.nodata_value = $inspection.nodata_value
    }
    if ($inspection.PSObject.Properties.Name -contains "resolution_x" -and $null -ne $inspection.resolution_x) {
        $assetVersionBody.resolution_x = $inspection.resolution_x
    }
    if ($inspection.PSObject.Properties.Name -contains "resolution_y" -and $null -ne $inspection.resolution_y) {
        $assetVersionBody.resolution_y = $inspection.resolution_y
    }

    $project = Encode-RsURIComponent $ProjectCode
    $importBody = @{
        requested_by       = $RequestedBy
        asset              = @{
            source_ref   = $inspection.storage_uri
            asset_type   = "raster"
            display_name = $ResultCode
            description  = "Imported by demo_import_and_slice.ps1"
            metadata     = @{
                demo_run = $run.RunID
            }
        }
        asset_version      = $assetVersionBody
        product            = @{
            code        = $ResultCode
            type        = $ResultType
            name        = $ResultCode
            description = "Demo managed result"
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

    $jobStates = @()
    if ($WaitForJobs) {
        foreach ($job in @($slice.jobs)) {
            $jobStates += Wait-RsJob -JobID $job.id
        }
    }

    $result = Invoke-RsApi -Method "GET" -Path ("/api/v1/results/{0}" -f $import.product.product.id)
    $delivery = $null
    if ($WaitForJobs) {
        $delivery = Invoke-RsApi -Method "GET" -Path ("/api/v1/projects/{0}/products/{1}/delivery?version=latest&style_version_id={2}" -f `
            (Encode-RsURIComponent $ProjectCode), `
            (Encode-RsURIComponent $ResultCode), `
            (Encode-RsURIComponent $styleVersion.id))
    }

    [ordered]@{
        run_id              = $run.RunID
        api_base_url        = Get-RsApiBaseUrl
        tile_base_url       = Get-RsTileBaseUrl
        input_path          = [System.IO.Path]::GetFullPath($InputPath)
        result_id           = $import.product.product.id
        result_code         = $ResultCode
        product_version_id  = $import.product_version.id
        style_version_id    = $styleVersion.id
        imported_asset_id   = $import.asset.id
        imported_version_id = $import.asset_version.id
        slice_job_ids       = @($slice.jobs | ForEach-Object { $_.id })
        slice_job_statuses  = @($jobStates | ForEach-Object {
            [ordered]@{
                id       = $_.id
                job_type = $_.job_type
                status   = $_.status
            }
        })
        cog_url             = if ($delivery) { $delivery.downloads.cog_url } else { $null }
        preview_url         = if ($delivery -and $delivery.preview) { $delivery.preview.url } else { $null }
        legend_url          = if ($delivery -and $delivery.legend) { $delivery.legend.url } else { $null }
        tileset_url         = if ($delivery -and $delivery.tileset) { $delivery.tileset.url_template } else { $null }
        result_status       = $result.status
        run_directory       = $run.RunDirectory
        log_directory       = if ($stack) { $stack.LogDirectory } else { $null }
    } | ConvertTo-Json -Depth 16
}
finally {
    if ($stack -and -not $KeepStack) {
        Stop-RsBackgroundProcesses -Processes $stack.Processes
    }
}
