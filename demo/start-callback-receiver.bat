@echo off
setlocal
chcp 65001 >nul 2>&1

set "ROOT=%~dp0"
if "%PYTHON_EXE%"=="" set "PYTHON_EXE=C:\Users\Administrator\.conda\envs\InSAR\python.exe"
if "%DEMO_CALLBACK_PORT%"=="" set "DEMO_CALLBACK_PORT=8920"
if "%DEMO_CALLBACK_PATH%"=="" set "DEMO_CALLBACK_PATH=/callback"
if "%DEMO_CALLBACK_OUTPUT_DIR%"=="" set "DEMO_CALLBACK_OUTPUT_DIR=%ROOT%..\temp\demo-callback"

if not exist "%PYTHON_EXE%" (
  echo Python interpreter not found: %PYTHON_EXE%
  exit /b 1
)

"%PYTHON_EXE%" "%ROOT%demo_callback_receiver.py" --host 127.0.0.1 --port %DEMO_CALLBACK_PORT% --path %DEMO_CALLBACK_PATH% --output-dir "%DEMO_CALLBACK_OUTPUT_DIR%"
