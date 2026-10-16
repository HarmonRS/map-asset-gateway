# API 调用文档

面向调用方（前端、下游系统、脚本）。服务默认监听 `http://127.0.0.1:8910`，
局域网开放方式见文末。

---

## 1. 认证

### 1.1 管理面（`/api/v1/admin/*`、`/admin/*`）

用**管理员凭据**，与业务令牌是两套东西：

| 方式 | 场景 | 配置 |
|---|---|---|
| HTTP Basic | 浏览器打开运维台 | `ADMIN_USER` + `ADMIN_PASSWORD` |
| Bearer | 脚本/接口调用 | `ADMIN_TOKEN` |

```bash
curl -u ops:change-me http://127.0.0.1:8910/api/v1/admin/basemaps
curl -H "Authorization: Bearer <ADMIN_TOKEN>" http://127.0.0.1:8910/api/v1/admin/basemaps
```

管理面可以签发令牌、改默认版本、配推送目标，所以**不要在局域网裸奔**：
`API_HOST` 绑非回环地址时若未配置任何管理员凭据，服务会**拒绝启动**。

### 1.2 业务面（编目 / 取图 / 切片）

用**服务令牌**（`bmp_` 开头，由管理面签发）。四种投递方式任选：

```
Authorization: Bearer <token>
X-Access-Token: <token>
?token=<token>
?access_token=<token>
```

查询参数形式是给地图客户端用的（`<img>`/瓦片库没法设请求头）。

令牌带 **scope**：`read`（默认，读已授权的底图/矢量）和 `slice`（提交切片任务）。
只带 `slice` 的令牌**不需要**任何底图授权，因此读不到任何瓦片。

```json
POST /api/v1/admin/tokens
{"name": "integration", "scopes": ["slice"]}
```

### 1.3 错误格式

所有错误统一：

```json
{"error": "token has no access to this basemap"}
```

| 状态码 | 含义 |
|---|---|
| 400 | 参数不合法 |
| 401 | 令牌缺失/无效/过期/停用 |
| 403 | 令牌有效，但没有该资源权限或缺少所需 scope |
| 404 | 资源或任务不存在 |
| 409 | 已有扫描在执行 |
| 500 | 服务端错误 |

---

## 2. 系统端点（无需认证）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/health` | 存活探针 |
| GET | `/ready` | 就绪探针 |
| GET | `/metrics` | Prometheus 文本指标（请求数、瓦片数/404、鉴权失败、运行时长） |
| GET | `/api/v1/system` | 服务信息（版本、各 base URL、扫描根目录等） |

---

## 3. 管理 API（需管理员凭据）

### 编目

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/admin/dashboard` | 运维台一次性数据（台账、令牌、推送、图层源清单等） |
| GET | `/api/v1/admin/basemaps` | 底图列表（含版本） |
| GET | `/api/v1/admin/vectors` | 矢量资源列表 |
| GET | `/api/v1/admin/scan-sources` | 扫描源列表 |
| POST | `/api/v1/admin/scan-sources` | 新增/更新扫描源 |
| POST | `/api/v1/admin/scan-sources/{code}/run` | 立即扫描该源 |
| POST | `/api/v1/admin/vectors/rescan` | 重扫矢量目录 |
| POST | `/api/v1/admin/basemaps/{code}/default-version` | 设默认版本，body `{"version":"2026-04"}` |
| POST | `/api/v1/admin/health/check` | 立即执行资源可用性巡检 |

巡检返回：

```json
{"checked_at":"2026-10-16T02:00:00Z","versions_checked":3,"versions_missing":0,
 "vectors_checked":1,"vectors_missing":0}
```

### 令牌

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/admin/tokens` | 令牌列表（只含前缀，不含明文） |
| POST | `/api/v1/admin/tokens` | 签发令牌 |
| POST | `/api/v1/admin/tokens/{id}/disable` | 停用 |

签发请求：

```json
{
  "name": "portal-a",
  "basemap_codes": ["gaode-image", "google-image"],
  "vector_codes": ["全国行政区.geojson", "landuse/parcel.shp"],
  "scopes": ["slice"],
  "expires_at": "2027-01-01T00:00:00Z"
}
```

- `vector_codes` 可写 code、相对路径或文件名
- `expires_at` 省略时用 `TOKEN_DEFAULT_TTL`（默认 30 天）；`0` 表示不过期
- 响应里的 `token` 字段是**明文，只返回这一次**，务必立即保存

### 推送

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/admin/targets` | 目标系统列表 |
| POST | `/api/v1/admin/targets` | 新增/更新目标系统 |
| POST | `/api/v1/admin/pushes` | 推送底图版本 `{target_code, basemap_code, version}` |
| POST | `/api/v1/admin/vector-pushes` | 推送矢量 `{target_code, vector_code}` |
| GET | `/api/v1/admin/push-records` | 底图推送记录（最近 20） |
| GET | `/api/v1/admin/vector-push-records` | 矢量推送记录 |
| POST | `/api/v1/admin/push-records/{id}/retry` | 手动重推 |
| POST | `/api/v1/admin/vector-push-records/{id}/retry` | 手动重推（矢量） |

推送**异步执行**，提交返回 `202` 与一条 `pending` 记录；失败按 30s/60s 退避
自动重试最多 3 次，状态在 `retrying` / `failed` / `succeeded` 之间变化。

---

## 4. 编目 API（需服务令牌，`read`）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/catalog/basemaps` | 该令牌可见的底图（已按授权过滤） |
| GET | `/api/v1/catalog/basemaps/{code}` | 单个底图详情 |
| GET | `/api/v1/catalog/vectors` | 可见的矢量资源 |
| GET | `/api/v1/catalog/vectors/{code}` | 单个矢量详情 |

底图详情里的 `url_template` 就是可直接使用的取图模板：

```text
http://<tile-base>/tiles/gaode-image/current/{z}/{x}/{y}.webp
```

---

## 5. 切片 API（需 `slice` scope 的令牌）

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/v1/slice/jobs` | 提交切片任务 → `202` + 任务对象 |
| GET | `/api/v1/slice/jobs/{id}` | 轮询单个任务 → `200` + 任务对象 |
| GET | `/api/v1/slice/layers/{code}/sources` | 该图层由哪些源影像构成 |

### 5.1 提交

```json
{
  "code": "uav-2026-flight-b",
  "name": "2026无人机影像 - 第二次飞行",
  "target_code": "uav-2026",
  "source_paths": ["D:\\uav\\flight-b.tif"],
  "zoom_min": 0,
  "zoom_max": 18,
  "tile_size": 256,
  "tile_format": "webp",
  "callback_url": "http://portal-a/api/tile-jobs/done",
  "requested_by": "ops"
}
```

字段说明：

| 字段 | 必填 | 说明 |
|---|---|---|
| `target_code` | 是 | 图层编码，**必须 ASCII**（中文会被归一化掉）。显示名由调用端自己映射 |
| `source_paths` | 是 | 源影像绝对路径数组；单元素时也可用 `source_path` |
| `code` | 否 | 幂等键。**同一个 code 重复提交会返回既有任务**，不会排队第二个 |
| `zoom_min` / `zoom_max` | 否 | 默认 0 / 0 |
| `tile_size` | 否 | 默认 256 |
| `tile_format` | 否 | 默认 webp（仅为元数据，实际格式由 gdal2tiles 决定） |
| `callback_url` | 否 | 任务结束时回调 |
| `requested_by` | 否 | 审计用 |

**两种模式，由源的数量决定：**

- **传 1 个源** → 直接切这一张，落到 `输出目录`，用于**日常增量累加**（新飞行覆盖到哪算哪）
- **传多个源** → 先 `gdalbuildvrt` 拼成一张 VRT 再切，**逐像素镶嵌，无瓦片接缝**，用于重建整个图层

累加示例（同一个 `target_code`，每次只提交新飞的）：

```bash
# 第一次
curl -X POST http://127.0.0.1:8910/api/v1/slice/jobs \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"target_code":"uav-2026","source_paths":["D:\\uav\\flight-a.tif"],"zoom_min":0,"zoom_max":18}'

# 几天后再飞，自动累加
curl -X POST http://127.0.0.1:8910/api/v1/slice/jobs \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"target_code":"uav-2026","source_paths":["D:\\uav\\flight-b.tif"],"zoom_min":0,"zoom_max":18}'
```

重建为无缝镶嵌：先取全部源，再一次性提交。

```bash
curl -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:8910/api/v1/slice/layers/uav-2026/sources
```

### 5.2 任务对象

```json
{
  "id": "265f65bb-b15b-40c2-a672-2103aea2c26d",
  "code": "uav-2026-flight-b",
  "name": "2026无人机影像 - 第二次飞行",
  "source_count": 1,
  "source_paths": ["D:\\uav\\flight-b.tif"],
  "target_code": "uav-2026",
  "output_root": "D:\\Code\\tile-server\\data\\tiles\\uav-2026",
  "zoom_min": 0, "zoom_max": 18,
  "tile_size": 256, "tile_format": "webp",
  "status": "pending",
  "message": "",
  "log_path": "",
  "callback_url": "http://portal-a/api/tile-jobs/done",
  "callback_status": "",
  "created_at": "2026-10-16T02:00:00Z"
}
```

`status`：`pending` → `running` → `succeeded` / `failed`。
失败原因在 `message`，完整 stdout/stderr 在 `log_path`。

任务**单并发**，按提交顺序执行，且不阻塞 API。

### 5.3 完成回调

任务结束时向 `callback_url` POST：

```json
{"event": "tile.job.finished", "job": { ...任务对象... }, "delivered": "2026-10-16T02:05:00Z"}
```

失败按 30s/60s 退避重试，最多 3 次；状态在任务的 `callback_status` /
`callback_attempts` / `callback_message` 里。

---

## 6. 取图与下载（需服务令牌）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/tiles/{code}/{version}/{z}/{x}/{y}.ext` | 指定版本取图 |
| GET | `/tiles/{code}/{z}/{x}/{y}.ext` | 兼容路由，取默认版本 |
| GET | `/vectors/{code}/download` | 下载矢量（shapefile 自动打包 zip） |
| GET | `/geojson/{相对路径}` | 兼容路由，按原始相对路径取 GeoJSON |
| GET | `/shp/{相对路径}` | 兼容路由，按相对路径取 shapefile（返回 zip） |

```bash
curl -o 0.webp "http://127.0.0.1:8910/tiles/gaode-image/current/0/0/0.webp?token=$TOKEN"
```

瓦片响应带 `Cache-Control: public, max-age=31536000, immutable`。

---

## 7. 局域网开放

默认 `API_HOST=127.0.0.1`，**只有本机能访问**。要让同局域网的其他机器打开运维台，
必须在 `config/service.env` 里同时改三处：

```ini
# 1) 监听所有网卡
API_HOST=0.0.0.0

# 2) 配管理员凭据 —— 浏览器打开运维台必须有 Basic 账号密码；
#    只配 ADMIN_TOKEN 的话浏览器没法带 Bearer，页面会 401
ADMIN_USER=ops
ADMIN_PASSWORD=<改掉它>

# 3) 对外地址改成局域网 IP 或主机名
API_BASE_URL=http://192.168.1.62:8910
TILE_PUBLIC_BASE_URL=http://192.168.1.62:8910
VECTOR_PUBLIC_BASE_URL=http://192.168.1.62:8910
```

改完重启服务，**并跑一次扫描**（`POST /api/v1/admin/scan-sources/local-basemaps/run`）——
台账里的 `url_template` 是扫描时用 base URL 拼出来的，不重扫的话对外仍然是旧的
`127.0.0.1` 地址。

还要放行防火墙入站：

```powershell
New-NetFirewallRule -DisplayName "Basemap Pusher 8910" -Direction Inbound `
  -Protocol TCP -LocalPort 8910 -Action Allow
```

之后局域网内访问 `http://192.168.1.62:8910/admin/`，浏览器会弹 Basic 认证框。