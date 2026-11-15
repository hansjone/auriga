@echo off
rem Uninstall entry in the install folder and the Start menu: opens the graphical uninstaller.
setlocal
set "UNINSTALL_SCRIPT=%~dp0uninstall.ps1"
cd /d "%TEMP%" || exit /b 1
"%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File "%UNINSTALL_SCRIPT%" %* & call exit /b %%ERRORLEVEL%%
