@echo off
setlocal
chcp 65001 >nul 2>&1
title Basemap Pusher Service

set "ROOT=%~dp0..\"
pushd "%ROOT%" >nul 2>&1
set "ROOT=%CD%\"
popd

set "EXE=%ROOT%bin\basemap-pusher.exe"
set "ENVFILE=%ROOT%config\service.env"

if not exist "%EXE%" (
  echo Service binary not found: %EXE%
  echo Build it first:  scripts\build-api.bat
  if "%NO_PAUSE%"=="" pause
  exit /b 1
)

set "VERB=%~1"
if "%VERB%"=="" set "VERB=status"

if /i "%VERB%"=="install"   goto do_install
if /i "%VERB%"=="uninstall" goto do_uninstall
if /i "%VERB%"=="start"     goto do_start
if /i "%VERB%"=="stop"      goto do_stop
if /i "%VERB%"=="restart"   goto do_restart
if /i "%VERB%"=="status"    goto do_status

echo Usage: service.bat ^<install^|uninstall^|start^|stop^|restart^|status^>
exit /b 2

:do_install
"%EXE%" --env-file "%ENVFILE%" install
goto finish

:do_uninstall
"%EXE%" uninstall
goto finish

:do_start
"%EXE%" start
goto finish

:do_stop
"%EXE%" stop
goto finish

:do_restart
"%EXE%" stop
"%EXE%" start
goto finish

:do_status
"%EXE%" status
goto finish

:finish
set "RC=%ERRORLEVEL%"
if not "%RC%"=="0" (
  echo.
  echo [FAIL] exit code %RC%.
  echo install / uninstall / start / stop need an elevated shell ^(Run as Administrator^).
)
if "%NO_PAUSE%"=="" pause
exit /b %RC%
