<#
.SYNOPSIS
    把一份已切好的 z/x/y 瓦片目录（例如大疆智图 terra_dom\tile）并入一个图层。

.DESCRIPTION
    做四件事：校验源目录、检查扩展名是否与现有图层冲突、拷贝进扫描根、
    写好 manifest.json，最后触发一次扫描。

    同一个图层只能有一种瓦片扩展名。服务发布时只会采用其中一个扩展名，
    若目录里混了 .png 和 .webp，另一种会全部 404 且不报错，所以这里直接拦住。

.EXAMPLE
    .\scripts\import-tiles.ps1 -Source "D:\Nie_WorkSpace\煤矿拍摄区域\lidars\terra_dom\tile" `
        -Code "coal-mine-dom" -Name "煤矿拍摄区域 DOM"

.EXAMPLE
    # 把第二批影像并进同一个图层（累加）
    .\scripts\import-tiles.ps1 -Source "D:\Nie_WorkSpace\另一个项目\terra_dom\tile" `
        -Code "uav-2026" -Name "2026无人机影像"
#>
param(
    [Parameter(Mandatory = $true)][string]$Source,
    [Parameter(Mandatory = $true)][string]$Code,
    [string]$Name = '',
    [string]$ApiBase = 'http://127.0.0.1:8910',
    [switch]$SkipScan
)

$ErrorActionPreference = 'Stop'

function Get-TileFormat {
    param([string]$Root)
    $zoom = Get-ChildItem -LiteralPath $Root -Directory -ErrorAction SilentlyContinue |
        Where-Object { $_.Name -match '^\d+$' } | Sort-Object { [int]$_.Name } | Select-Object -First 1
    if (-not $zoom) { return $null }
    foreach ($x in (Get-ChildItem -LiteralPath $zoom.FullName -Directory -ErrorAction SilentlyContinue)) {
        $file = Get-ChildItem -LiteralPath $x.FullName -File -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($file) { return $file.Extension.TrimStart('.').ToLower() }
    }
    return $null
}

function Get-ZoomRange {
    param([string]$Root)
    $zooms = Get-ChildItem -LiteralPath $Root -Directory -ErrorAction SilentlyContinue |
        Where-Object { $_.Name -match '^\d+$' } | ForEach-Object { [int]$_.Name } | Sort-Object
    if (-not $zooms) { return $null }
    return @($zooms[0], $zooms[-1])
}

$Root = Split-Path -Parent $PSScriptRoot
$TileRoot = Join-Path $Root 'data\tiles'
$Dest = Join-Path $TileRoot $Code

if (-not (Test-Path -LiteralPath $Source)) { throw "源目录不存在：$Source" }
$Source = (Resolve-Path -LiteralPath $Source).Path
if (-not (Test-Path -LiteralPath $TileRoot)) { throw "扫描根目录不存在：$TileRoot" }

Write-Host "源目录 : $Source"
Write-Host "目标   : $Dest"

# 1) 源必须是 z/x/y 目录树
$srcZooms = Get-ZoomRange $Source
if (-not $srcZooms) {
    throw "源目录下没有数字命名的级别目录（形如 12/ 13/），不像是 z/x/y 切片树。"
}
Write-Host ("源级别 : z{0}-{1}" -f $srcZooms[0], $srcZooms[1])

# 2) 探测扩展名，并和现有图层比对
$srcFormat = Get-TileFormat $Source
if (-not $srcFormat) { throw "在源目录里找不到瓦片文件：$Source" }

$existingFormat = $null
if (Test-Path -LiteralPath $Dest) { $existingFormat = Get-TileFormat $Dest }

if ($existingFormat -and $existingFormat -ne $srcFormat) {
    throw @"
格式冲突，已中止。

  图层 $Code 现有瓦片 : .$existingFormat
  本次要导入的瓦片    : .$srcFormat

同一个图层只能有一种扩展名：服务发布 URL 时只会采用其中一个，
另一种会全部 404 且不会报错。请改用同格式的源，或换一个 -Code。
"@
}
Write-Host ("瓦片格式: .{0}{1}" -f $srcFormat, $(if ($existingFormat) { "（与现有图层一致）" } else { "" }))

# 3) 拷贝（robocopy 0-7 都算成功）
Write-Host "`n开始拷贝……"
robocopy $Source $Dest /E /NFL /NDL /NP /R:1 /W:1 /MT:8 | Out-Null
$rc = $LASTEXITCODE
if ($rc -ge 8) { throw "robocopy 失败，退出码 $rc" }

# 4) 写 manifest（保留已有显示名，除非显式传了 -Name）
$manifestPath = Join-Path $Dest 'manifest.json'
$oldName = ''
$isDefault = $true
if (Test-Path -LiteralPath $manifestPath) {
    try {
        $old = Get-Content -Raw -LiteralPath $manifestPath | ConvertFrom-Json
        $oldName = $old.name
        if ($null -ne $old.is_default) { $isDefault = [bool]$old.is_default }
    } catch { }
}
if (-not $Name) { $Name = if ($oldName) { $oldName } else { $Code } }

$combined = Get-ZoomRange $Dest
$manifest = [ordered]@{
    code        = $Code
    name        = $Name
    type        = 'xyz'
    description = '由 import-tiles.ps1 导入的已切好瓦片'
    version     = 'current'
    status      = 'ready'
    tile_format = $srcFormat
    tile_scheme = 'xyz'
    root_path   = '.'
    is_default  = $isDefault
}
($manifest | ConvertTo-Json -Depth 5) | Set-Content -LiteralPath $manifestPath -Encoding UTF8
Write-Host ("manifest: code={0} name={1} z{2}-{3} .{4}" -f $Code, $Name, $combined[0], $combined[1], $srcFormat)

# 5) 触发扫描（管理接口只接受本机请求，这里走 127.0.0.1）
if ($SkipScan) {
    Write-Host "`n已跳过扫描（-SkipScan）。记得在运维台点一次「立即扫描」。"
    exit 0
}

Write-Host "`n触发扫描……"
try {
    $result = curl.exe -s -X POST "$ApiBase/api/v1/admin/scan-sources/local-basemaps/run"
    Write-Host $result
} catch {
    Write-Warning "自动扫描失败（$($_.Exception.Message)）。服务未启动时属正常，请稍后在运维台点「立即扫描」。"
}

Write-Host "`n完成。图层 $Code 现在包含 z$($combined[0])-$($combined[1]) 的 .$srcFormat 瓦片。"