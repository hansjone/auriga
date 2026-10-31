@echo off
node "%~dp0collect.mjs" %*
exit /b %errorlevel%
