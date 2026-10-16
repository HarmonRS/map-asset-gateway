param(
    [string]$EnvFile = (Join-Path $PSScriptRoot "..\config\service.env")
)

$script:RsDemoRepoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$script:RsDemoProcesses = @()
$script:RsDemoEnvLoaded = $false

function Get-RsRepoRoot {
    return $script:RsDemoRepoRoot
}

function Import-RsDemoEnvironment {
    param([string]$Path = $EnvFile)

    if ($script:RsDemoEnvLoaded) {
        return
    }

    if (Test-Path -LiteralPath $Path) {
        foreach ($rawLine in (Get-Content -LiteralPath $Path)) {
            $line = $rawLine.Trim()
            if (-not $line -or $line.StartsWith("#")) {
                continue
            }
            if ($line.StartsWith("export ")) {
                $line = $line.Substring(7).Trim()
            }
            $parts = $line.Split("=", 2)
            if ($parts.Count -ne 2) {
                continue
            }
            $key = $parts[0].Trim()
            $value = $parts[1].Trim().Trim("`"").Trim("'")
            if (-not $key) {
                continue
            }
            if (-not (Test-Path "Env:$key")) {
                Set-Item -Path "Env:$key" -Value $value
            }
        }
    }

    if (-not $env:PYTHON_EXE) {
        $env:PYTHON_EXE = "C:\Users\Administrator\.conda\envs\InSAR\python.exe"
    }
    if (-not $env:API_BASE_URL) {
        $env:API_BASE_URL = "http://127.0.0.1:8910"
    }
    if (-not $env:TILE_STATIC_BASE_URL) {
        $env:TILE_STATIC_BASE_URL = "http://127.0.0.1:8900"
    }
    if (-not $env:RS_API_BASE_URL) {
        $env:RS_API_BASE_URL = $env:API_BASE_URL
    }
    if (-not $env:PYTHONIOENCODING) {
        $env:PYTHONIOENCODING = "utf-8"
    }
    if (-not $env:NO_PAUSE) {
        $env:NO_PAUSE = "1"
    }

    $script:RsDemoEnvLoaded = $true
}

function Get-RsApiBaseUrl {
    Import-RsDemoEnvironment
    return ([string]$env:API_BASE_URL).TrimEnd("/")
}

function Get-RsTileBaseUrl {
    Import-RsDemoEnvironment
    return ([string]$env:TILE_STATIC_BASE_URL).TrimEnd("/")
}

function Get-RsPythonExe {
    Import-RsDemoEnvironment
    if (-not (Test-Path -LiteralPath $env:PYTHON_EXE)) {
        throw "Python interpreter not found: $($env:PYTHON_EXE)"
    }
    return $env:PYTHON_EXE
}

function Get-RsGoCommand {
    return (Get-Command go -ErrorAction Stop).Source
}

function New-RsRunContext {
    param([string]$Prefix = "demo")

    $runId = "{0}_{1}" -f $Prefix, (Get-Date -Format "yyyyMMdd_HHmmss")
    $runDir = Join-Path (Get-RsRepoRoot) ("temp\demo-runs\" + $runId)
    $logDir = Join-Path $runDir "logs"
    $outputDir = Join-Path $runDir "output"

    New-Item -ItemType Directory -Force -Path $runDir | Out-Null
    New-Item -ItemType Directory -Force -Path $logDir | Out-Null
    New-Item -ItemType Directory -Force -Path $outputDir | Out-Null

    return [pscustomobject]@{
        RunID           = $runId
        RunDirectory    = $runDir
        LogDirectory    = $logDir
        OutputDirectory = $outputDir
    }
}

function ConvertTo-RsFileUri {
    param([Parameter(Mandatory)] [string]$Path)

    $fullPath = [System.IO.Path]::GetFullPath($Path)
    return ([System.Uri]$fullPath).AbsoluteUri
}

function Encode-RsURIComponent {
    param([Parameter(Mandatory)] [string]$Value)

    return [System.Uri]::EscapeDataString($Value)
}

function Wait-RsUrl {
    param(
        [Parameter(Mandatory)] [string]$Url,
        [int]$TimeoutSec = 60
    )

    $deadline = (Get-Date).AddSeconds($TimeoutSec)
    do {
        try {
            Invoke-RestMethod -Uri $Url -Method Get -TimeoutSec 5 | Out-Null
            return
        } catch {
            Start-Sleep -Milliseconds 500
        }
    } while ((Get-Date) -lt $deadline)

    throw "Timed out waiting for $Url"
}

function Invoke-RsApi {
    param(
        [Parameter(Mandatory)] [string]$Method,
        [Parameter(Mandatory)] [string]$Path,
        [object]$Body,
        [int]$TimeoutSec = 30
    )

    Import-RsDemoEnvironment

    $uri = if ($Path -match "^https?://") {
        $Path
    } else {
        "{0}{1}" -f (Get-RsApiBaseUrl), $Path
    }

    $params = @{
        Uri        = $uri
        Method     = $Method
        TimeoutSec = $TimeoutSec
        Headers    = @{
            Accept = "application/json"
        }
    }

    if ($PSBoundParameters.ContainsKey("Body") -and $null -ne $Body) {
        $params.ContentType = "application/json"
        $params.Body = ($Body | ConvertTo-Json -Depth 32)
    }

    try {
        $response = Invoke-RestMethod @params
    } catch {
        $message = $_.Exception.Message
        $webResponse = $_.Exception.Response
        if ($webResponse) {
            try {
                $stream = $webResponse.GetResponseStream()
                if ($stream) {
                    $reader = New-Object System.IO.StreamReader($stream)
                    $rawBody = $reader.ReadToEnd()
                    $reader.Dispose()
                    if ($rawBody) {
                        try {
                            $errorEnvelope = $rawBody | ConvertFrom-Json
                            if ($errorEnvelope.error) {
                                $message = "{0} {1}" -f $errorEnvelope.error.code, $errorEnvelope.error.message
                            } else {
                                $message = $rawBody
                            }
                        } catch {
                            $message = $rawBody
                        }
                    }
                }
            } catch {
            }
        }
        throw "API call failed: $Method $uri`n$message"
    }

    if ($null -eq $response) {
        return $null
    }
    if ($response.error) {
        throw "API error: $Method $uri => $($response.error.code) $($response.error.message)"
    }
    return $response.data
}

function Wait-RsJob {
    param(
        [Parameter(Mandatory)] [string]$JobID,
        [int]$TimeoutSec = 300,
        [int]$PollIntervalSec = 2
    )

    $deadline = (Get-Date).AddSeconds($TimeoutSec)
    do {
        $job = Invoke-RsApi -Method "GET" -Path ("/api/v1/jobs/{0}" -f $JobID)
        if ($job.status -eq "succeeded") {
            return $job
        }
        if ($job.status -in @("failed", "cancelled")) {
            throw "Job $JobID finished with status=$($job.status) error=$($job.last_error_message)"
        }
        Start-Sleep -Seconds $PollIntervalSec
    } while ((Get-Date) -lt $deadline)

    throw "Timed out waiting for job $JobID"
}

function Get-RsStyleByCode {
    param(
        [Parameter(Mandatory)] [string]$ProjectCode,
        [Parameter(Mandatory)] [string]$Code,
        [switch]$AllowMissing
    )

    $project = Encode-RsURIComponent $ProjectCode
    $keyword = Encode-RsURIComponent $Code
    $items = @(Invoke-RsApi -Method "GET" -Path "/api/v1/styles?project_code=$project&keyword=$keyword")
    $match = $items | Where-Object { $_.code -eq $Code } | Select-Object -First 1
    if ($match) {
        return $match
    }
    if ($AllowMissing) {
        return $null
    }
    throw "Style not found: $Code"
}

function Get-RsTargetSystemByCode {
    param(
        [Parameter(Mandatory)] [string]$ProjectCode,
        [Parameter(Mandatory)] [string]$Code,
        [switch]$AllowMissing
    )

    $project = Encode-RsURIComponent $ProjectCode
    $items = @(Invoke-RsApi -Method "GET" -Path "/api/v1/target-systems?project_code=$project")
    $match = $items | Where-Object { $_.code -eq $Code } | Select-Object -First 1
    if ($match) {
        return $match
    }
    if ($AllowMissing) {
        return $null
    }
    throw "Target system not found: $Code"
}

function Get-RsResultByCode {
    param(
        [Parameter(Mandatory)] [string]$ProjectCode,
        [Parameter(Mandatory)] [string]$Code,
        [switch]$AllowMissing
    )

    $project = Encode-RsURIComponent $ProjectCode
    $keyword = Encode-RsURIComponent $Code
    $items = @(Invoke-RsApi -Method "GET" -Path "/api/v1/projects/$project/results?keyword=$keyword")
    $match = $items | Where-Object { $_.code -eq $Code } | Select-Object -First 1
    if ($match) {
        return Invoke-RsApi -Method "GET" -Path ("/api/v1/results/{0}" -f $match.id)
    }
    if ($AllowMissing) {
        return $null
    }
    throw "Result not found: $Code"
}

function New-RsDemoStyleSpec {
    param(
        [string]$StyleID = "demo_continuous_style",
        [string]$Name = "Demo Continuous Style",
        [string]$Scope = "project",
        [double]$MinValue = -1.0,
        [double]$MaxValue = 1.0,
        [int]$TileSize = 256,
        [string]$TileFormat = "webp",
        [int]$MinZoom = 0,
        [int]$MaxZoom = 4
    )

    return @{
        spec_version = "1.0"
        id           = $StyleID
        name         = $Name
        version      = 1
        scope        = $Scope
        render       = @{
            mode         = "continuous"
            band         = 1
            resampling   = "bilinear"
            value_domain = @{
                min = $MinValue
                max = $MaxValue
            }
            color_map    = @{
                type       = "linear"
                stops      = @(
                    @{ value = $MinValue; color = "#8c510a"; label = "low" },
                    @{ value = (($MinValue + $MaxValue) / 2.0); color = "#f6e8c3"; label = "mid" },
                    @{ value = $MaxValue; color = "#01665e"; label = "high" }
                )
                null_color = "#00000000"
            }
        }
        legend       = @{
            title       = $Name
            orientation = "horizontal"
            precision   = 2
        }
        output       = @{
            tile_format = $TileFormat
            tile_size   = $TileSize
            min_zoom    = $MinZoom
            max_zoom    = $MaxZoom
        }
    }
}

function Ensure-RsStyleVersion {
    param(
        [Parameter(Mandatory)] [string]$ProjectCode,
        [Parameter(Mandatory)] [string]$StyleCode,
        [string]$StyleName = "Demo Continuous Style",
        [hashtable]$StyleSpec,
        [switch]$CreateNewVersion
    )

    $style = Get-RsStyleByCode -ProjectCode $ProjectCode -Code $StyleCode -AllowMissing
    if (-not $style) {
        $style = Invoke-RsApi -Method "POST" -Path "/api/v1/styles" -Body @{
            project_code = $ProjectCode
            scope        = "project"
            code         = $StyleCode
            name         = $StyleName
            created_by   = "demo"
        }
    }

    if (-not $CreateNewVersion -and $style.latest_version_id) {
        return Invoke-RsApi -Method "GET" -Path ("/api/v1/style-versions/{0}" -f $style.latest_version_id)
    }

    if (-not $StyleSpec) {
        $StyleSpec = New-RsDemoStyleSpec -StyleID $StyleCode -Name $StyleName
    }

    return Invoke-RsApi -Method "POST" -Path ("/api/v1/styles/{0}/versions" -f $style.id) -Body @{
        style_spec = $StyleSpec
    }
}

function Ensure-RsTargetSystem {
    param(
        [Parameter(Mandatory)] [string]$ProjectCode,
        [Parameter(Mandatory)] [string]$Code,
        [Parameter(Mandatory)] [string]$Name,
        [Parameter(Mandatory)] [string]$CallbackUrl,
        [string]$CallbackMethod = "POST",
        [hashtable]$CallbackHeaders,
        [int]$TimeoutSeconds = 30,
        [bool]$Enabled = $true,
        [string]$CreatedBy = "demo"
    )

    $body = @{
        code             = $Code
        name             = $Name
        callback_url     = $CallbackUrl
        callback_method  = $CallbackMethod
        timeout_seconds  = $TimeoutSeconds
        enabled          = $Enabled
        callback_headers = $CallbackHeaders
    }

    $existing = Get-RsTargetSystemByCode -ProjectCode $ProjectCode -Code $Code -AllowMissing
    if ($existing) {
        return Invoke-RsApi -Method "PATCH" -Path ("/api/v1/target-systems/{0}" -f $existing.id) -Body $body
    }

    $body.project_code = $ProjectCode
    $body.created_by = $CreatedBy
    return Invoke-RsApi -Method "POST" -Path "/api/v1/target-systems" -Body $body
}

function Start-RsBackgroundProcess {
    param(
        [Parameter(Mandatory)] [string]$FilePath,
        [string[]]$ArgumentList,
        [Parameter(Mandatory)] [string]$WorkingDirectory,
        [Parameter(Mandatory)] [string]$Name,
        [Parameter(Mandatory)] [string]$LogDirectory
    )

    New-Item -ItemType Directory -Force -Path $LogDirectory | Out-Null

    $stdout = Join-Path $LogDirectory ($Name + ".stdout.log")
    $stderr = Join-Path $LogDirectory ($Name + ".stderr.log")
    $process = Start-Process -FilePath $FilePath `
        -ArgumentList $ArgumentList `
        -WorkingDirectory $WorkingDirectory `
        -PassThru `
        -RedirectStandardOutput $stdout `
        -RedirectStandardError $stderr

    $script:RsDemoProcesses += $process
    return $process
}

function Stop-RsBackgroundProcesses {
    param([object[]]$Processes = $script:RsDemoProcesses)

    foreach ($process in $Processes) {
        if ($process -and -not $process.HasExited) {
            Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
        }
    }
}

function Start-RsDemoStack {
    param(
        [int]$ApiPort = 28910,
        [int]$TilePort = 28900,
        [switch]$IncludeWorker,
        [string]$RunID = ("stack_" + (Get-Date -Format "yyyyMMdd_HHmmss"))
    )

    Import-RsDemoEnvironment

    $runDir = Join-Path (Get-RsRepoRoot) ("temp\demo-runs\" + $RunID)
    $logDir = Join-Path $runDir "logs"
    New-Item -ItemType Directory -Force -Path $runDir | Out-Null
    New-Item -ItemType Directory -Force -Path $logDir | Out-Null

    $apiBaseUrl = "http://127.0.0.1:$ApiPort"
    $tileBaseUrl = "http://127.0.0.1:$TilePort"

    $env:API_HOST = "127.0.0.1"
    $env:API_PORT = "$ApiPort"
    $env:API_BASE_URL = $apiBaseUrl
    $env:TILE_HOST = "127.0.0.1"
    $env:TILE_PORT = "$TilePort"
    $env:TILE_STATIC_BASE_URL = $tileBaseUrl
    $env:RS_API_BASE_URL = $apiBaseUrl
    $env:NO_PAUSE = "1"

    $pythonExe = Get-RsPythonExe
    $goExe = Get-RsGoCommand

    $tileProcess = Start-RsBackgroundProcess `
        -FilePath $pythonExe `
        -ArgumentList @((Join-Path (Get-RsRepoRoot) "server.py")) `
        -WorkingDirectory (Get-RsRepoRoot) `
        -Name "tile-server" `
        -LogDirectory $logDir

    $apiProcess = Start-RsBackgroundProcess `
        -FilePath $goExe `
        -ArgumentList @("run", ".\cmd\api") `
        -WorkingDirectory (Join-Path (Get-RsRepoRoot) "services\api-go") `
        -Name "api-service" `
        -LogDirectory $logDir

    Wait-RsUrl -Url "$tileBaseUrl/health" -TimeoutSec 90
    Wait-RsUrl -Url "$apiBaseUrl/ready" -TimeoutSec 120

    $workerProcess = $null
    if ($IncludeWorker) {
        $workerProcess = Start-RsBackgroundProcess `
            -FilePath $pythonExe `
            -ArgumentList @((Join-Path (Get-RsRepoRoot) "services\worker-py\main.py")) `
            -WorkingDirectory (Get-RsRepoRoot) `
            -Name "python-worker" `
            -LogDirectory $logDir
        Start-Sleep -Seconds 1
    }

    $processes = @($tileProcess, $apiProcess)
    if ($workerProcess) {
        $processes += $workerProcess
    }

    return [pscustomobject]@{
        RunID        = $RunID
        RunDirectory = $runDir
        LogDirectory = $logDir
        ApiBaseUrl   = $apiBaseUrl
        TileBaseUrl  = $tileBaseUrl
        Processes    = $processes
    }
}

function Start-RsCallbackReceiver {
    param(
        [int]$Port = 28920,
        [string]$Path = "/callback",
        [string]$RunID = ("callback_" + (Get-Date -Format "yyyyMMdd_HHmmss"))
    )

    $runDir = Join-Path (Get-RsRepoRoot) ("temp\demo-runs\" + $RunID)
    $logDir = Join-Path $runDir "logs"
    $outputDir = Join-Path $runDir "callback-records"
    New-Item -ItemType Directory -Force -Path $logDir | Out-Null
    New-Item -ItemType Directory -Force -Path $outputDir | Out-Null

    $pythonExe = Get-RsPythonExe
    $process = Start-RsBackgroundProcess `
        -FilePath $pythonExe `
        -ArgumentList @(
            (Join-Path $PSScriptRoot "demo_callback_receiver.py"),
            "--host", "127.0.0.1",
            "--port", "$Port",
            "--path", $Path,
            "--output-dir", $outputDir
        ) `
        -WorkingDirectory (Get-RsRepoRoot) `
        -Name "callback-receiver" `
        -LogDirectory $logDir

    $callbackUrl = "http://127.0.0.1:$Port$Path"
    Wait-RsUrl -Url "http://127.0.0.1:$Port/health" -TimeoutSec 30

    return [pscustomobject]@{
        RunID           = $RunID
        RunDirectory    = $runDir
        LogDirectory    = $logDir
        OutputDirectory = $outputDir
        CallbackURL     = $callbackUrl
        Process         = $process
    }
}

function Get-RsLatestCallbackRecord {
    param([Parameter(Mandatory)] [string]$OutputDirectory)

    if (-not (Test-Path -LiteralPath $OutputDirectory)) {
        return $null
    }

    $file = Get-ChildItem -LiteralPath $OutputDirectory -Filter "*.json" |
        Sort-Object LastWriteTimeUtc -Descending |
        Select-Object -First 1

    if (-not $file) {
        return $null
    }

    return [pscustomobject]@{
        Path    = $file.FullName
        Payload = (Get-Content -LiteralPath $file.FullName -Raw | ConvertFrom-Json)
    }
}
