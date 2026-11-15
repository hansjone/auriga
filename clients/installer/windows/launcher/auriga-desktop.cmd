@echo off
rem Older launchers resume this file at byte 244 after an update: keep the exit line there.
goto main
rem ---------------------------------------------------------------------------------------------------------------------------
exit /b %ERRORLEVEL%
:main
setlocal
set "ROOT=%~dp0.."
set /p CUR=<"%ROOT%\current.txt"
set "VER=%ROOT%\versions\%CUR%"
rem Without arguments start the tray app; it owns DSH and stops it cleanly on exit.
if "%~1"=="" if exist "%VER%\auriga\tray\AurigaDesktop.exe" start "" "%VER%\auriga\tray\AurigaDesktop.exe" & exit /b 0
rem Long commands end on the same line so an update that replaces this file is never re-read.
if /I "%~1"=="diagnostics" "%VER%\upstream\node\node.exe" "%VER%\auriga\runtime\cli.mjs" %* --root "%ROOT%" & call exit /b %%ERRORLEVEL%%
"%VER%\upstream\node\node.exe" "%VER%\auriga\runtime\cli.mjs" launch --root "%ROOT%" -- %* & call exit /b %%ERRORLEVEL%%
