@echo off
setlocal enableextensions
REM Run gdal2tiles.py from the GDAL bundled with QGIS.
REM
REM QGIS ships GDAL but its Python bindings need the OSGeo4W environment
REM (GDAL_DATA, PROJ_LIB, PYTHONHOME). Running gdal2tiles.exe from a bare
REM shell fails to import osgeo, so this wrapper loads o4w_env.bat first.
REM
REM Override auto-detection with:  set GDAL2TILES_QGIS_ROOT=C:\path\to\QGIS

set "QGIS_ROOT=%GDAL2TILES_QGIS_ROOT%"
if not "%QGIS_ROOT%"=="" goto have_root

for /f "delims=" %%D in ('dir /b /ad /o-n "%ProgramFiles%\QGIS *" 2^>nul') do (
  if not defined QGIS_ROOT set "QGIS_ROOT=%ProgramFiles%\%%D"
)

:have_root
if "%QGIS_ROOT%"=="" (
  echo gdal2tiles wrapper: QGIS not found. Set GDAL2TILES_QGIS_ROOT to the QGIS install directory. 1>&2
  exit /b 2
)

if not exist "%QGIS_ROOT%\bin\o4w_env.bat" (
  echo gdal2tiles wrapper: "%QGIS_ROOT%\bin\o4w_env.bat" is missing; is this a QGIS install? 1>&2
  exit /b 2
)
if not exist "%QGIS_ROOT%\apps\Python312\Scripts\gdal2tiles.exe" (
  echo gdal2tiles wrapper: gdal2tiles.exe not found under "%QGIS_ROOT%\apps\Python312\Scripts". 1>&2
  exit /b 2
)

call "%QGIS_ROOT%\bin\o4w_env.bat" >nul 2>&1
"%QGIS_ROOT%\apps\Python312\Scripts\gdal2tiles.exe" %*
exit /b %ERRORLEVEL%
