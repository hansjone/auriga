@echo off
setlocal
cd /d "%~dp0"
if defined AURIGA_CLIENT_DEVKIT (
  "%AURIGA_CLIENT_DEVKIT%\build-node\node.exe" "%~dp0scripts\client-release\cli.mjs" %*
) else (
  node "%~dp0scripts\client-release\cli.mjs" %*
)
exit /b %errorlevel%
