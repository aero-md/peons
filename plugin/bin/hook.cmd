@echo off
rem Lance « peons.exe hook » (l'événement arrive sur stdin). Sert au SessionStart : le
rem premier démarrage lance ce hook en même temps que fetch.cmd, avant que le
rem téléchargement de l'exe soit fini.
set "EXE=%LOCALAPPDATA%\peons\peons.exe"
if not exist "%EXE%" (
  echo peons : erreur normale au premier lancement, peons.exe est encore en cours de telechargement. Les evenements suivants passeront. 1>&2
  exit /b 1
)
"%EXE%" hook
exit /b %errorlevel%
