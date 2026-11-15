@echo off
goto main
rem -----------------------------------------------------------------------------------------------------------------------------------------------------------------------
exit /b %ERRORLEVEL%
:main
setlocal
"%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -ExecutionPolicy Bypass -File "%~dp0rollback.ps1" & call exit /b %%ERRORLEVEL%%
