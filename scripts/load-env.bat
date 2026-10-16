@echo off
set "ENV_FILE=%~1"
if "%ENV_FILE%"=="" set "ENV_FILE=%~dp0..\config\service.env"
if not exist "%ENV_FILE%" exit /b 0

for /f "usebackq eol=# tokens=1,* delims==" %%A in ("%ENV_FILE%") do (
    if not "%%~A"=="" call :set_if_missing "%%~A" "%%~B"
)
exit /b 0

:set_if_missing
call set "__current=%%%~1%%"
if defined __current goto :clear_and_exit
set "%~1=%~2"

:clear_and_exit
set "__current="
exit /b 0
