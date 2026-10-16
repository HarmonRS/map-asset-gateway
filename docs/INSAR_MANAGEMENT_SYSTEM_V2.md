# Insar_management_system_v2 接入说明

本文档说明 `Z:\Code\Insar_management_system_v2` 如何接入当前 `tile-server`，不需要修改推送服务外部的公开匿名路由，全部走带 token 的受保护接口。

## 结论

当前 `tile-server` 经过兼容后，可以支撑 `Insar_management_system_v2` 的底图和 GeoJSON/SHP 调用，但前提是目标系统把原先的固定静态路径改成带 token 的调用方式。

支持范围：

- 底图瓦片
- GeoJSON 文件直连读取
- Shapefile 或 `.zip` 方式下载

不需要目标系统实现“接收推送”的回调接口。

## 推荐调用方式

假设服务地址为：

```text
http://127.0.0.1:8910
```

假设访问 token 为：

```text
bmp_xxxxxxxxxxxxxxxxx
```

### 1. 底图瓦片

推荐直接使用默认版本兼容路由：

```text
GET /tiles/{code}/{z}/{x}/{y}.ext?token=...
```

示例：

```text
http://127.0.0.1:8910/tiles/google_image/{z}/{x}/{y}.webp?token=bmp_xxx
http://127.0.0.1:8910/tiles/gaode_image/{z}/{x}/{y}.webp?token=bmp_xxx
http://127.0.0.1:8910/tiles/gaode_shp/{z}/{x}/{y}.webp?token=bmp_xxx
```

说明：

- `google_image`、`gaode_image`、`gaode_shp` 可以直接保留下划线写法
- 服务内部会归一化到对应底图库 code
- 路由会自动解析该底图的默认版本

如果你希望显式锁定版本，也可以继续使用：

```text
GET /tiles/{code}/{version}/{z}/{x}/{y}.ext?token=...
```

### 2. GeoJSON

推荐直接按原始相对路径访问：

```text
GET /geojson/{relative/path}.geojson?token=...
```

示例：

```text
http://127.0.0.1:8910/geojson/全国行政区.geojson?token=bmp_xxx
```

说明：

- 支持按原始相对路径访问
- 也支持按文件名访问
- 也支持按内部 vector code 访问
- 返回内容为实际 GeoJSON 文件内容，可直接 `fetch(...).json()`

### 3. SHP

推荐按原始相对路径下载：

```text
GET /shp/{relative/path}.shp?token=...
```

示例：

```text
http://127.0.0.1:8910/shp/landuse/parcel.shp?token=bmp_xxx
```

说明：

- 如果底层资源是 `.shp + .dbf + .shx ...` 组合，服务会自动打包成 zip 返回
- 如果底层资源本来就是 `.zip`，则直接返回 zip

如果你希望继续按内部 code 下载，也可以使用：

```text
GET /vectors/{code}/download?token=...
```

## Token 传递方式

支持以下方式，推荐前端场景使用查询参数：

### 查询参数

```text
?token=bmp_xxx
```

### Header

```text
Authorization: Bearer bmp_xxx
```

或：

```text
X-Access-Token: bmp_xxx
```

## 前端改造建议

### 底图常量

把原先：

```js
`${TILE_BASE}/tiles/google_image/{z}/{x}/{y}.webp`
```

改成：

```js
`${TILE_BASE}/tiles/google_image/{z}/{x}/{y}.webp?token=${encodeURIComponent(TILE_TOKEN)}`
```

其他底图同理。

### 行政区 GeoJSON

把原先：

```js
`${TILE_BASE}/geojson/全国行政区.geojson`
```

改成：

```js
`${TILE_BASE}/geojson/全国行政区.geojson?token=${encodeURIComponent(TILE_TOKEN)}`
```

## 推荐接入流程

1. 在 `tile-server` 管理页或管理 API 扫描底图目录和矢量目录。
2. 创建一个专供 `Insar_management_system_v2` 使用的 token。
3. token 授权对应底图和需要访问的 GeoJSON/SHP 资源。
4. 在 `Insar_management_system_v2` 中把静态地址改成带 token 的服务地址。

## 创建 token 时的资源填写规则

### 底图

填写底图库 code，例如：

```text
google-image
gaode-image
gaode-shp
```

### 矢量

现在既可以填内部 code，也可以直接填原始路径，例如：

```text
全国行政区.geojson
landuse/parcel.shp
```

## 适配边界

当前服务负责的是：

- 扫描
- 目录管理
- token 鉴权
- 底图瓦片输出
- GeoJSON/SHP 输出

当前服务不负责：

- 生成切片
- 把矢量转换成 GeoJSON
- 修改 `Insar_management_system_v2` 代码
