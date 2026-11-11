@echo off
setlocal
set "ROOT=%~dp0.."
set /p CUR=<"%ROOT%\current.txt"
"%ROOT%\versions\%CUR%\upstream\node\node.exe" "%ROOT%\versions\%CUR%\auriga\runtime\cli.mjs" launch --root "%ROOT%" -- %*
exit /b %ERRORLEVEL%
