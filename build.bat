@echo off
cd /d "%~dp0server"
go build -trimpath -ldflags "-s -w" -o ..\PersonalityColorTest.exe .
if %errorlevel%==0 (
  echo Build OK: PersonalityColorTest.exe
) else (
  echo Build FAILED - check Go installation
)
pause
