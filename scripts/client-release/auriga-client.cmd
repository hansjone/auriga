@echo off
setlocal
set PSModulePath=
if not defined AURIGA_CLIENT_DEVKIT goto missing
if not defined AURIGA_CLIENT_NODE_SHA256 goto missing
"%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -NonInteractive -Command "$ErrorActionPreference='Stop'; if ($env:AURIGA_CLIENT_NODE_SHA256 -notmatch '^[a-f0-9]{64}$') {exit 1}; $node=Join-Path $env:AURIGA_CLIENT_DEVKIT 'build-node/node.exe'; if ((Get-FileHash -Algorithm SHA256 -LiteralPath $node).Hash -ine $env:AURIGA_CLIENT_NODE_SHA256) {exit 1}"
if errorlevel 1 exit /b 1
"%AURIGA_CLIENT_DEVKIT%\build-node\node.exe" "%~dp0cli.mjs" %*
exit /b %errorlevel%
:missing
echo Set AURIGA_CLIENT_DEVKIT and independently approved AURIGA_CLIENT_NODE_SHA256. 1>&2
exit /b 1
