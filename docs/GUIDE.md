# 使用指南

按**任务**走的操作手册。接口字段速查见 [API.md](./API.md)。

本指南以本机部署为例：

- 服务地址：`http://192.168.1.62:8910`（静态 IP，局域网可达）
- 运维台：`http://127.0.0.1:8910/admin/`（**仅本机**）
- 数据目录：`D:\Code\tile-server\data`（切片在 `tiles\`，矢量在 `geojson\`）

---

## 一、五分钟跑通：拿到第一张图

### 1. 在运维台签发一个令牌

浏览器打开 `http://127.0.0.1:8910/admin/` → **访问令牌** → 签发。

- **底图编码**：填已入库的 code，例如 `gaode-image`
- **有效时长**：留空走默认 30 天
- 勾不勾"允许提交切片任务"，决定这个令牌能不能调切片接口

签完页面顶部会显示**明文令牌**，形如 `bmp_xxxxxxxx`。**只显示这一次**，立刻复制存好。

### 2. 确认底图已入库

运维台 **底图** 页能看到 `gaode-image` / `gaode-shp` / `google-image`，并显示级别范围和格式。

取图模板长这样：

```text
http://192.168.1.62:8910/tiles/gaode-image/current/{z}/{x}/{y}.webp
```

### 3. 取一张验证

```powershell
curl.exe -o 0.webp -w "%{http_code} %{content_type}`n" `
  "http://192.168.1.62:8910/tiles/gaode-image/current/0/0/0.webp?token=<令牌>"
```

返回 `200 image/webp` 就通了。

### 4. 放进地图前端

取图模板本身就是前端要的格式，两种用法：

**Leaflet**

```javascript
const token = 'bmp_xxxxxxxx';
const urlTemplate = 'http://192.168.1.62:8910/tiles/gaode-image/current/{z}/{x}/{y}.webp';

L.tileLayer(urlTemplate + '?token=' + token, {
  tileSize: 256,
  minZoom: 0,
  maxZoom: 13,          // 用底图详情里的 maxZoom
  attribution: '内部底图'
}).addTo(map);
```

**OpenLayers**

```javascript
new ol.layer.Tile({
  source: new ol.source.XYZ({
    url: 'http://192.168.1.62:8910/tiles/gaode-image/current/{z}/{x}/{y}.webp?token=bmp_xxxxxxxx',
    crossOrigin: 'anonymous'
  })
});
```

> 令牌走查询参数（`?token=`）是给地图客户端设计的——`<img>` 和瓦片库没法设请求头。
> 代价是令牌会出现在 URL 里，所以**给前端用的令牌要单独签发、单独设短有效期**，别和管理令牌混用。

### 5. 也可以让程序自己去问目录

不要让前端写死 code，让它先查目录：

```powershell
curl.exe -H "Authorization: Bearer <令牌>" http://192.168.1.62:8910/api/v1/catalog/basemaps
```

返回里每个底图带 `url_template`、`min_zoom`、`max_zoom`、`tile_format`，
前端照着渲染即可。目录会**按令牌授权自动过滤**——没有权限的底图不会出现。

---

## 二、无人机影像：一次次的累加

目标：一个图层 `uav-2026`，飞一次加一次，最终是一张完整的图。

### 准备

1. 运维台 **访问令牌** 签发一个勾了 **"允许提交切片任务"** 的令牌
2. 这个令牌不需要任何底图授权——它只用来提交切片，读不到图

### 第一次飞行

```powershell
$TOKEN = "bmp_xxxxxxxx"

curl.exe -X POST http://127.0.0.1:8910/api/v1/slice/jobs `
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" `
  -d '{"target_code":"uav-2026","source_paths":["D:\\uav\\flight-a.tif"],"zoom_min":0,"zoom_max":18}'
```

返回 `202` 和任务对象，`status` 是 `pending`。

### 几天后又飞了

**同一个 `target_code`，只提交新影像**，就是累加：

```powershell
curl.exe -X POST http://127.0.0.1:8910/api/v1/slice/jobs `
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" `
  -d '{"target_code":"uav-2026","source_paths":["D:\\uav\\flight-b.tif"],"zoom_min":0,"zoom_max":18}'
```

新影像覆盖到哪算哪，之前飞的地方原样保留（命令里带了 `--exclude`，空白瓦片不会落盘，
所以不会把旧影像"挖出洞"）。

### 看进度

```powershell
curl.exe -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8910/api/v1/slice/jobs/<任务id>
```

`status` 走 `pending → running → succeeded / failed`。
失败时 `message` 有原因，完整输出在 `log_path`。

### 什么时候需要"重建"

日常累加是**按瓦片**覆盖的：如果两次飞行在**同一张 256px 瓦片内**有重叠，
整块瓦片会来自较新的那次，可能出现瓦片边界的可见接缝。

**重叠很小的话不用管。** 真觉得有接缝时，做一次镶嵌重建：

运维台 → **影像切片** → 图层源清单 → 点该图层的 **「重建为镶嵌」**。

它会自动读出这个图层记录的全部源影像、沿用当前已发布的级别范围，
先 `gdalbuildvrt` 拼成一张虚拟栅格再切，重叠区**逐像素**合成，没有接缝。

### 图层现在由哪些影像构成

```powershell
curl.exe -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8910/api/v1/slice/layers/uav-2026/sources
```

运维台同一页也有这张清单。

### 两个注意点

- **`target_code` 必须是英文数字**（如 `uav-2026`）。中文会被归一化掉导致报错。
  想显示"2026无人机影像"，由**你的前端自己映射** code → 中文名。
- 切片是**单并发**的，按提交顺序执行。一次提交多个图层要排队，但不会阻塞接口。

---

### 另一种做法：直接导入大疆智图的切片

大疆智图自己就会输出切片（`terra_dom\tile\` 下是标准的 `z/x/y.png`，
坐标方案是 **XYZ**，和本服务一致）。如果你在智图里看过、效果满意、
不需要重做镶嵌，那就**没必要再切一遍**——直接把它的切片并进来。

用 `scripts\import-tiles.ps1`：

```powershell
.\scripts\import-tiles.ps1 `
  -Source "D:\Nie_WorkSpace\煤矿拍摄区域\lidars\terra_dom\tile" `
  -Code "coal-mine-dom" `
  -Name "煤矿拍摄区域 DOM"
```

脚本做四件事：校验源目录 → 检查扩展名冲突 → 拷贝进扫描根 → 写好 manifest 并扫描。

**累加**：同一个 `-Code` 再来一次就是累加，和切片输出落在一起：

```powershell
.\scripts\import-tiles.ps1 -Source "D:\...\第二个项目\terra_dom\tile" `
  -Code "uav-2026" -Name "2026无人机影像"
```

**为什么要用脚本而不是手拷**：服务发布取图 URL 时，**瓦片扩展名是取"随便找到的第一个文件"**。
同一个图层里如果混了 `.png` 和 `.webp`，模板只会发布其中一种，另一种**全部 404 且不报错**。
脚本会在拷贝前拦住这种情况。级别范围不一致没关系（自动取并集），**扩展名必须统一**。

**两种做法怎么选**

| 情况 | 做法 |
|---|---|
| 智图里看着满意，不需要重做 | 导入切片（本节），快 |
| 要累加、要能"一键重建为镶嵌" | 切片 `dom.tif`（上一节），有源清单 |
| 同一次数据既想快又要可重建 | 导入切片发布 + 把 `dom.tif` 也提交一次切片登记为源 |

> 导入的切片是"成品"，`layer_sources` 里不会记录它的原图，
> 所以**这类图层用不了控制台的"重建为镶嵌"**。想要那个能力就得走切片。
## 三、给下游系统对接

### 方式一：下游主动拉（推荐）

下游拿一个令牌，调目录 → 拿 `url_template` → 自己取图。

```powershell
curl.exe -H "Authorization: Bearer <令牌>" http://192.168.1.62:8910/api/v1/catalog/basemaps
```

好处：地址由服务方维护，换 IP / 加图层下游不用改代码。

### 方式二：服务方推送描述

在运维台 **推送** 页配好目标系统（回调地址 + 可选请求头），
资源更新时把描述推给下游，下游收到后再自己来拉。

- 推送是**异步**的，提交后先返回 `pending`
- 失败自动重试（30s、60s），状态在推送记录里看
- 失败记录可以点 **重推**

### 切片完成回调

提交切片任务时带上 `callback_url`：

```json
{"target_code":"uav-2026","source_paths":["D:\\uav\\b.tif"],"callback_url":"http://portal-a/api/tile-jobs/done"}
```

任务结束时服务会 POST：

```json
{"event":"tile.job.finished","job":{ ...任务对象... }}
```

失败同样自动退避重试。

---

## 四、常见问题速查

| 现象 | 原因 | 怎么办 |
|---|---|---|
| `401 invalid token` | 令牌错、过期或被停用 | 运维台令牌页看状态；重新签发 |
| `403 token has no access` | 令牌有效但没授权这个底图 | 用有该底图授权的令牌，或重新签发 |
| `403 admin endpoints are loopback-only` | 从别的机器调了管理接口 | 管理接口只能在本机调；远程请用业务令牌走 `/api/v1/catalog`、`/tiles` |
| `403 ... slice scope` | 令牌没勾"允许提交切片任务" | 重新签发并勾上 |
| `404` 取图 | 该 z/x/y 没切出来，或资源已下线 | 运维台 **底图** 页看该版本的"在位/文件缺失"标记 |
| 取图地址里是 `127.0.0.1` | 改过 base URL 但没重扫 | 运维台点 **立即扫描**，模板是扫描时生成的 |
| 切片任务 `failed` | 源影像路径不对、不是栅格、GDAL 报错 | 看任务的 `message` 和 `log_path` |
| 页面打不开 | 在别的机器访问了 `/admin/` | 管理台仅本机；远程只能调业务接口 |
| 别的网段访问不通 | 路由器没有两个网段之间的路由 | 找网络管理员加路由 |

---

## 五、日常运维

运维台各页对应的动作：

| 页面 | 用途 |
|---|---|
| **总览** | 待处理事项（失败的扫描/推送、缺失的资源）+ 实时指标 |
| **底图** | 台账、默认版本、取图模板、**就地验真**（粘贴令牌试拉一张） |
| **矢量** | 矢量资源与下载地址，同样可验真 |
| **令牌** | 签发 / 停用，查看授权范围与最近使用时间 |
| **推送** | 目标系统、推送记录、失败重推 |
| **扫描** | 扫描源、立即扫描、扫描历史 |
| **影像切片** | 提交切片、任务列表、图层源清单与重建 |
| **服务** | 版本、对外地址、探针与指标入口 |

**故障排查第一站是日志**：

```powershell
Get-Content D:\Code\tile-server\bin\logs\basemap-pusher.log -Tail 50
```

访问日志每行形如 `GET /tiles/xxx/... 200 320B 2ms`，令牌已脱敏成 `***`。

**服务管理**（`install/uninstall/start/stop` 需管理员）：

```powershell
scripts\service.bat status
scripts\service.bat restart
```