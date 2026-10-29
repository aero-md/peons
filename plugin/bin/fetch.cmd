@echo off
rem Installe peons.exe dans %LOCALAPPDATA%\peons depuis la dernière release GitHub.
rem   fetch.cmd          ne fait rien (aucune requête réseau) si peons.exe est déjà là
rem   fetch.cmd force    télécharge la dernière release et remplace l'exe en place
rem Le SHA-256 de l'exe est vérifié contre peons.exe.sha256, publié à côté dans la release.
setlocal
set "DIR=%LOCALAPPDATA%\peons"
set "EXE=%DIR%\peons.exe"
if not defined PEONS_RELEASE_URL set "PEONS_RELEASE_URL=https://github.com/aero-md/peons/releases/latest/download"

if /i not "%~1"=="force" if exist "%EXE%" exit /b 0

if not exist "%DIR%" mkdir "%DIR%"
curl.exe -fsSL -o "%EXE%.new" "%PEONS_RELEASE_URL%/peons.exe" || goto download
curl.exe -fsSL -o "%EXE%.sha256" "%PEONS_RELEASE_URL%/peons.exe.sha256" || goto download

set "WANT="
set /p WANT=<"%EXE%.sha256"
for /f "tokens=1" %%h in ("%WANT%") do set "WANT=%%h"
set "GOT="
for /f "skip=1 delims=" %%h in ('certutil -hashfile "%EXE%.new" SHA256') do if not defined GOT set "GOT=%%h"
set "GOT=%GOT: =%"
if not defined WANT goto mismatch
if /i not "%GOT%"=="%WANT%" goto mismatch

rem Windows refuse d'écraser un exe qui tourne, mais accepte de le renommer
if exist "%EXE%" (
  del "%DIR%\peons.old.exe" >nul 2>&1
  move /y "%EXE%" "%DIR%\peons.old.exe" >nul || goto busy
)
move /y "%EXE%.new" "%EXE%" >nul || goto busy
del "%EXE%.sha256" >nul 2>&1
echo peons.exe installe dans %DIR% : relancez l'application pour utiliser la nouvelle version.
exit /b 0

:download
echo peons : telechargement impossible depuis %PEONS_RELEASE_URL% 1>&2
goto fail
:mismatch
echo peons : SHA-256 incorrect (attendu "%WANT%", obtenu "%GOT%"), exe refuse. 1>&2
goto fail
:busy
echo peons : impossible de remplacer %EXE% 1>&2
:fail
del "%EXE%.new" >nul 2>&1
exit /b 1
