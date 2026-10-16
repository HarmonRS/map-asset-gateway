@echo off
setlocal
chcp 65001 >nul 2>&1
title Build Basemap Pusher

set "ROOT=%~dp0..\\"
pushd "%ROOT%" >nul 2>&1
set "ROOT=%CD%\"
popd
call "%ROOT%scripts\load-env.bat" "%ROOT%config\service.env"

REM Keep the toolchain caches inside the repo, the same way run-api.bat does.
REM Without this, go build uses the global module cache and reaches out to
REM proxy.golang.org, which is blocked on many internal networks.
if "%GOCACHE%"=="" set "GOCACHE=%ROOT%.gocache"
if "%GOMODCACHE%"=="" set "GOMODCACHE=%ROOT%.gomodcache"
if "%GOTMPDIR%"=="" set "GOTMPDIR=%ROOT%.gotmp"
if "%GOPATH%"=="" set "GOPATH=%ROOT%.gopath"
if "%GOPROXY%"=="" set "GOPROXY=https://goproxy.cn,direct"
if "%GOSUMDB%"=="" set "GOSUMDB=off"

set "OUT=%ROOT%bin\basemap-pusher.exe"
if not exist "%ROOT%bin" mkdir "%ROOT%bin" >nul 2>&1

echo ========================================
echo   Building Basemap Pusher
echo ========================================
echo Output: %OUT%
echo.

pushd "%ROOT%services\api-go"
go build -o "%OUT%" .\cmd\api
set "BUILD_EXIT=%ERRORLEVEL%"
popd

if not "%BUILD_EXIT%"=="0" (
  echo.
  echo [FAIL] Build failed with exit code %BUILD_EXIT%.
) else (
  echo.
  echo [OK] Built %OUT%
  echo.
  echo Install as a Windows service ^(elevated shell^):
  echo   "%OUT%" --env-file "%ROOT%config\service.env" install
  echo   "%OUT%" start
)

if "%NO_PAUSE%"=="" pause
exit /b %BUILD_EXIT%
