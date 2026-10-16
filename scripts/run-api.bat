@echo off
setlocal
chcp 65001 >nul 2>&1
title Basemap Pusher

set "ROOT=%~dp0..\\"
call "%ROOT%scripts\load-env.bat" "%ROOT%config\service.env"

if "%GOCACHE%"=="" set "GOCACHE=%ROOT%.gocache"
if "%GOMODCACHE%"=="" set "GOMODCACHE=%ROOT%.gomodcache"
if "%GOTMPDIR%"=="" set "GOTMPDIR=%ROOT%.gotmp"
if "%GOPATH%"=="" set "GOPATH=%ROOT%.gopath"
if "%GOPROXY%"=="" set "GOPROXY=https://goproxy.cn,direct"
if "%GOSUMDB%"=="" set "GOSUMDB=off"
if "%API_HOST%"=="" set "API_HOST=127.0.0.1"
if "%API_PORT%"=="" set "API_PORT=8910"
if "%API_BASE_URL%"=="" set "API_BASE_URL=http://127.0.0.1:%API_PORT%"
if "%TILE_PUBLIC_BASE_URL%"=="" set "TILE_PUBLIC_BASE_URL=%API_BASE_URL%"
if "%VECTOR_PUBLIC_BASE_URL%"=="" set "VECTOR_PUBLIC_BASE_URL=%API_BASE_URL%"
if "%SQLITE_PATH%"=="" set "SQLITE_PATH=%ROOT%data\basemap-pusher.db"
if "%BASEMAP_SCAN_ROOT%"=="" set "BASEMAP_SCAN_ROOT=%ROOT%data\tiles"
if "%VECTOR_SCAN_ROOT%"=="" set "VECTOR_SCAN_ROOT=%ROOT%data\geojson"

echo ========================================
echo   Basemap Pusher
echo ========================================
echo.
echo Env file: %ROOT%config\service.env
echo Listen: %API_BASE_URL%
echo Tile base: %TILE_PUBLIC_BASE_URL%
echo Vector base: %VECTOR_PUBLIC_BASE_URL%
echo SQLite: %SQLITE_PATH%
echo Scan root: %BASEMAP_SCAN_ROOT%
echo Vector root: %VECTOR_SCAN_ROOT%
echo.

pushd "%ROOT%services\api-go"
go run .\cmd\api
popd

if "%NO_PAUSE%"=="" pause
