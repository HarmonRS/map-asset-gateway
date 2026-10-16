# Basemap Pusher

An internal catalog and push service for existing basemap tiles, GeoJSON files, and shapefiles.

The service does not generate tiles, does not rebuild vector data, and does not bring back the old result-management pipeline. It only does:

- scan existing basemap tile directories
- scan existing vector files
- manage metadata
- issue tokens
- control token access to basemaps and vector assets
- push basemap or vector descriptors to downstream systems

## Stack

- Go
- SQLite
- standard library HTTP server

## Current Capabilities

- scan `BASEMAP_SCAN_ROOT` into SQLite
- scan `VECTOR_SCAN_ROOT` into SQLite
- support two basemap directory styles
  - manifest-based directories
  - raw `z/x/y.ext` tile roots
- support vector asset formats
  - `.geojson`
  - `.json`
  - `.shp` plus sidecar files
  - `.zip` shapefile packages
- token-protected basemap catalog, tile access, vector catalog, and vector download
- token-protected compatibility routes for default-version tiles, GeoJSON paths, and shapefile paths
- admin panel at `GET /admin/`
- downstream callback push for basemaps and vectors

## Basemap Scan Rules

### Manifest-based directory

```text
data/tiles/gaode-image/
  manifest.json
  tiles/
    0/0/0.png
    1/0/0.png
```

Example `manifest.json`:

```json
{
  "code": "gaode-image",
  "name": "Gaode Image",
  "type": "xyz",
  "version": "2026-04",
  "status": "ready",
  "tile_format": "png",
  "tile_scheme": "xyz",
  "min_zoom": 0,
  "max_zoom": 18,
  "root_path": "tiles",
  "is_default": true
}
```

### Raw tile directory

```text
data/tiles/google-image/
  0/0/0.webp
  1/0/0.webp
```

This is imported automatically as:

- `code = directory name`
- `version = current`
- `type = xyz`

## Vector Scan Rules

The service scans `VECTOR_SCAN_ROOT` recursively.

Examples:

```text
data/geojson/admin-boundary.geojson
data/geojson/roads.zip
data/geojson/landuse/parcel.shp
data/geojson/landuse/parcel.dbf
data/geojson/landuse/parcel.shx
```

Rules:

- `.geojson` and `.json` are imported as `geojson`
- `.zip` is imported as `shpzip`
- `.shp` is imported as `shapefile`
- shapefile downloads are served as zip packages on demand

## Token Model

A token can be granted access to:

- one or more basemaps
- one or more vector assets

Supported token delivery:

- `Authorization: Bearer <token>`
- `X-Access-Token: <token>`
- query parameter `token=`
- query parameter `access_token=`

## API

- [docs/GUIDE.md](./docs/GUIDE.md) — task-oriented walkthrough: first tile,
  integrating with a map frontend, accumulating drone imagery over several
  flights, downstream integration and troubleshooting.
- [docs/API.md](./docs/API.md) — endpoint reference: auth, every route grouped by
  purpose, slicing request/job shapes, error codes, LAN exposure checklist.

### System

- `GET /health`
- `GET /ready`
- `GET /metrics` (Prometheus text: request, tile, tile-miss and auth counters)
- `GET /api/v1/system`

### Admin

- `GET /admin/`
- `GET /api/v1/admin/dashboard`
- `GET /api/v1/admin/basemaps`
- `GET /api/v1/admin/vectors`
- `GET /api/v1/admin/scan-sources`
- `POST /api/v1/admin/scan-sources`
- `POST /api/v1/admin/scan-sources/{code}/run`
- `POST /api/v1/admin/vectors/rescan`
- `POST /api/v1/admin/basemaps/{code}/default-version`
- `GET /api/v1/admin/targets`
- `POST /api/v1/admin/targets`
- `GET /api/v1/admin/tokens`
- `POST /api/v1/admin/tokens`
- `POST /api/v1/admin/tokens/{id}/disable`
- `GET /api/v1/admin/push-records`
- `POST /api/v1/admin/pushes`
- `GET /api/v1/admin/vector-push-records`
- `POST /api/v1/admin/vector-pushes`

### Catalog and Downloads

- `GET /api/v1/catalog/basemaps`
- `GET /api/v1/catalog/basemaps/{code}`
- `GET /api/v1/catalog/vectors`
- `GET /api/v1/catalog/vectors/{code}`
- `GET /tiles/{code}/{z}/{x}/{y}.ext`
- `GET /tiles/{code}/{version}/{z}/{x}/{y}.ext`
- `GET /geojson/{relative/path/to/file.geojson}`
- `GET /shp/{relative/path/to/file.shp}`
- `GET /vectors/{code}/download`

Examples:

```text
GET /tiles/gaode_image/10/843/388.png?token=bmp_xxx
GET /tiles/gaode-image/current/10/843/388.png?token=bmp_xxx
GET /geojson/全国行政区.geojson?token=bmp_xxx
GET /shp/landuse/parcel.shp?token=bmp_xxx
GET /vectors/admin-boundary/download?token=bmp_xxx
```

Compatibility route notes:

- `/tiles/{code}/{z}/{x}/{y}.ext` resolves the current default version of the basemap
- `/geojson/{...}` resolves vector assets by original relative path, file name, or internal code
- `/shp/{...}` returns `.shp` packages as zip downloads and also supports `.zip` vector packages
- token creation accepts vector references by code or by original relative path

## Slicing API for Other Systems

Integrations call a narrow, non-admin surface:

```text
POST /api/v1/slice/jobs                     submit a job  -> 202 + the job object
GET  /api/v1/slice/jobs/{id}                poll one job  -> 200 + the job object
GET  /api/v1/slice/layers/{code}/sources    the layer's source rasters
```

All three require a service token carrying the **`slice`** scope. Create one
through the admin API or console; a slice-only token needs no basemap or vector
grants, so it cannot read any tiles:

```json
POST /api/v1/admin/tokens
{"name": "integration", "scopes": ["slice"]}
```

- **Accumulating layers** — a layer is the union of its source rasters, tracked in
  `layer_sources`. Submit **one** new source to append a flight (fast, what
  overlaps wins per tile), or submit **all** of `GET /api/v1/slice/layers/{code}/sources`
  to rebuild the layer as a VRT mosaic (per-pixel, no tile seams). The console's
  影像切片 view lists each layer's sources.
- `TILE_JOB_COMMAND` slices one input; `TILE_JOB_VRT_COMMAND` runs first when
  several sources are submitted. `{inputs}` expands to one argument per source,
  and `{output_vrt}` is the temporary mosaic. Both ship as wrappers over the GDAL
  bundled with QGIS (`scripts/gdal2tiles-qgis.bat`,
  `scripts/gdalbuildvrt-qgis.bat`) because the Python bindings need the OSGeo4W
  environment; set `GDAL2TILES_QGIS_ROOT` when QGIS is not in `%ProgramFiles%`.
  The slicer default passes `--exclude`, so a new flight cannot punch transparent
  holes into the previous imagery.
- **Rebuild from the console** — 影像切片 → 图层源清单 → 重建为镶嵌 slices every
  recorded source into one VRT, reusing the zoom range the layer is already
  published at. No need to copy paths by hand.
- **Known limit** — a layer's source list only grows (there is no delete API yet),
  and a rebuild never clears the output directory. If a source is ever removed by
  hand, tiles it used to cover but no longer does stay behind; delete the layer
  directory and rebuild to clean up.
- **Idempotent submit** — sending the same `code` twice returns the existing job
  instead of queueing a second one, so a retrying caller is safe.
- **Completion callback** — pass `callback_url` when submitting and the service
  POSTs `{"event":"tile.job.finished","job":{...}}` when the job ends. Failures
  retry on the same 30s/60s backoff as push callbacks.
- Jobs run one at a time, oldest first; the admin console's 影像切片 view shows
  the same queue.
## Resource Health, Retries and Slicing

- **Resource health** — every catalogued tile root and vector file is stat'ed on
  `HEALTH_CHECK_INTERVAL` and on demand (`POST /api/v1/admin/health/check`).
  A path that vanished is recorded as `missing` and surfaces in the console's
  待处理 list instead of waiting for a client 404. Health is tracked separately
  from `status`: status is a catalog fact, health is whether the bytes are on
  disk right now.
- **Push retries** — a failed callback is retried up to 3 times with backoff
  (30s, 60s) and stays `retrying` while queued; the schedule lives in the
  database so a restart does not lose it. A finished record can be re-queued
  with `POST /api/v1/admin/push-records/{id}/retry` (or
  `.../vector-push-records/{id}/retry`).
- **Image slicing** — `scripts/gdal2tiles-qgis.bat` runs the GDAL bundled with
  QGIS (its Python bindings need the OSGeo4W environment, so a bare
  `gdal2tiles.exe` will not work; set `GDAL2TILES_QGIS_ROOT` if QGIS is not in
  `%ProgramFiles%`). `POST /api/v1/admin/tile-jobs` queues a job that runs
  `TILE_JOB_COMMAND` (see [config/service.env.example](./config/service.env.example))
  and, on success, rescans so the sliced basemap appears in the catalog. The
  service does the orchestration; the raster work is done by GDAL. The service
  account needs write access to the basemap scan root.
## Running as a Windows Service

The binary manages its own service registration — no NSSM or WinSW wrapper:

```text
basemap-pusher.exe --env-file <path\to\config\service.env> install
basemap-pusher.exe start
basemap-pusher.exe status
basemap-pusher.exe stop
basemap-pusher.exe uninstall
```

- `install` bakes `--env-file` into the service command line, because the SCM
  starts services in `C:\Windows\System32` and the app would otherwise miss its
  config. Without `--env-file` it looks for `config/service.env` next to the
  executable and up to two directories above it.
- A service has no console, so the standard logger is redirected to
  `bin\logs\basemap-pusher.log`, rolling to `.1` at 10 MB. Override the path with
  the `SERVICE_LOG_FILE` environment variable. Without this the service would
  discard every log line.
- Recovery actions are registered on install: restart after 5s, 15s, then 60s,
  retrying for a day. The service starts automatically at boot.
- `install` / `uninstall` / `start` / `stop` need an **elevated** console
  (Run as Administrator); `status` and the console mode do not.
- Started with no verb the binary runs as a normal console app and handles
  Ctrl+C / SIGTERM with a 15s drain. Under the SCM the service handler owns the
  same shutdown path.

Build the binary the service will run (it must not depend on the Go toolchain):

```text
scripts\build-api.bat
```

The script loads `config/service.env` first, so the Go caches and `GOPROXY`
match the rest of the project instead of the global defaults. Running `go build`
by hand from the repository root will fail: the module lives in
`services\api-go`, not at the root.
## Access Control

The admin console and `/api/v1/admin/*` can mint tokens that read every
basemap, so they are guarded:

- configure `ADMIN_USER` + `ADMIN_PASSWORD` for the browser console (HTTP Basic)
- configure `ADMIN_TOKEN` for API clients (`Authorization: Bearer <token>`)
- with neither set, the service **refuses to start** unless `API_HOST` is a
  loopback address (`127.0.0.1`, `::1`, `localhost`)
- the admin surface is **loopback-only by default**, so the service can bind
  `0.0.0.0` to serve tiles on the LAN while the console stays reachable only
  from this machine; `ADMIN_ALLOW_REMOTE=true` opens it up and then requires
  credentials

Cross-origin requests are denied by default; add the exact origins you trust to
`CORS_ALLOWED_ORIGINS`. Client tokens never appear in redirect URLs, and tokens
carried in a query string are redacted from the access log.
## Config

See [config/service.env.example](./config/service.env.example).

For `Insar_management_system_v2` integration, see [docs/INSAR_MANAGEMENT_SYSTEM_V2.md](./docs/INSAR_MANAGEMENT_SYSTEM_V2.md).

Important variables:

- `API_HOST`
- `API_PORT`
- `API_BASE_URL`
- `TILE_PUBLIC_BASE_URL`
- `VECTOR_PUBLIC_BASE_URL`
- `SQLITE_PATH`
- `BASEMAP_SCAN_ROOT`
- `VECTOR_SCAN_ROOT`
- `AUTO_SCAN_ON_START`
- `ADMIN_USER` / `ADMIN_PASSWORD` (HTTP Basic for the browser console)
- `ADMIN_TOKEN` (bearer token for admin API clients)
- `CORS_ALLOWED_ORIGINS` (comma-separated allow-list; empty means no CORS)
- `TOKEN_DEFAULT_TTL` (lifetime for new tokens, e.g. `720h`; `0` disables expiry)
- `HEALTH_CHECK_INTERVAL` (resource availability pass, e.g. `5m`; `0` disables)
- `TILE_JOB_COMMAND` (image slicing command template; needs GDAL)
- `TILE_JOB_LOG_DIR` (per-job log directory; empty = `<scan root>/.tile-jobs`)

## Start

### Windows service (production)

The service is the supported way to run this on Windows — it starts at boot,
restarts on failure and keeps its own log file:

```text
scripts\build-api.bat
scripts\service.bat install     (elevated)
scripts\service.bat start
scripts\service.bat status
```

`service.bat` accepts `install | uninstall | start | stop | restart | status`
and forwards to `bin\basemap-pusher.exe`. The install / uninstall / start / stop
verbs need an **elevated** shell; `status` does not.

The old `start-all.bat` / `stop-all.bat` launchers were removed: they killed the
process by port with `taskkill`, which fought the service's restart policy.

### Console (development)

```text
scripts\run-api.bat
```

This runs `go run` with the project's own caches, for a quick local loop. It is
not supervised and does not survive a reboot.

## Database Tables

- `basemaps`
- `basemap_versions`
- `scan_sources`
- `scan_runs`
- `target_systems`
- `service_tokens`
- `token_grants`
- `push_records`
- `vector_assets`
- `vector_token_grants`
- `vector_push_records`

## Design Boundaries

- no Python worker
- no PostgreSQL
- no tile generation pipeline
- no result artifact management
- no raster processing jobs

GIS software still produces the tiles and vector files. This project only scans, catalogs, authorizes, downloads, and pushes descriptors.