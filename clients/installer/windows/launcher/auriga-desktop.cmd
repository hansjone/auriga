@echo off
setlocal
set "ROOT=%~dp0.."
set /p CUR=<"%ROOT%\current.txt"
if /I "%~1"=="diagnostics" goto diagnostics
"%ROOT%\versions\%CUR%\upstream\node\node.exe" "%ROOT%\versions\%CUR%\auriga\runtime\cli.mjs" launch --root "%ROOT%" -- %*
exit /b %ERRORLEVEL%
:diagnostics
"%ROOT%\versions\%CUR%\upstream\node\node.exe" "%ROOT%\versions\%CUR%\auriga\runtime\cli.mjs" %* --root "%ROOT%"
exit /b %ERRORLEVEL%
