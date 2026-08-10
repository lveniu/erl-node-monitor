@echo off
chcp 65001 >nul
cd /d "%~dp0"
start "Erlang Monitoring Platform" powershell.exe -NoLogo -NoExit -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\start-local-monitor.ps1"
exit /b 0
