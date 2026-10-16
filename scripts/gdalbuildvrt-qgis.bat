@echo off
setlocal enableextensions
REM Run gdalbuildvrt from the GDAL bundled with QGIS.
REM
REM QGIS ships GDAL but its Python bindings need the OSGeo4W environment
REM (GDAL_DATA, PROJ_LIB). Loading o4w_env.bat first keeps PROJ working.
REM
REM Override auto-detection with:  set GDAL2TILES_QGIS_ROOT=C:\path\to\QGIS

set "QGIS_ROOT=%GDAL2TILES_QGIS_ROOT%"
if not "%QGIS_ROOT%"=="" goto have_root

for /f "delims=" %%D in ('dir /b /ad /o-n "%ProgramFiles%\QGIS *" 2^>nul') do (
  if not defined QGIS_ROOT set "QGIS_ROOT=%ProgramFiles%\%%D"
)

:have_root
if "%QGIS_ROOT%"=="" (
  echo gdalbuildvrt wrapper: QGIS not found. Set GDAL2TILES_QGIS_ROOT to the QGIS install directory. 1>&2
  exit /b 2
)
if not exist "%QGIS_ROOT%\bin\o4w_env.bat" (
  echo gdalbuildvrt wrapper: "%QGIS_ROOT%\bin\o4w_env.bat" is missing; is this a QGIS install? 1>&2
  exit /b 2
)
if not exist "%QGIS_ROOT%\bin\gdalbuildvrt.exe" (
  echo gdalbuildvrt wrapper: gdalbuildvrt.exe not found under "%QGIS_ROOT%\bin". 1>&2
  exit /b 2
)

call "%QGIS_ROOT%\bin\o4w_env.bat" >nul 2>&1
"%QGIS_ROOT%\bin\gdalbuildvrt.exe" %*
exit /b %ERRORLEVEL%
