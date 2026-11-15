@echo off
goto main
rem -----------------------------------------------------------------------------------------------------------------------------------------------------------------------
exit /b %ERRORLEVEL%
:main
setlocal
set "ROOT=%~dp0.."
set /p CUR=<"%ROOT%\current.txt"
"%ROOT%\versions\%CUR%\upstream\node\node.exe" "%ROOT%\versions\%CUR%\auriga\runtime\cli.mjs" rollback --root "%ROOT%" & call exit /b %%ERRORLEVEL%%
