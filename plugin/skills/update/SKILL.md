---
description: Met à jour peons.exe depuis la dernière release GitHub
disable-model-invocation: true
shell: powershell
allowed-tools: PowerShell(& *fetch.cmd force)
---

!`& "${CLAUDE_PLUGIN_ROOT}/bin/fetch.cmd" force`

Recopie la sortie ci-dessus. Si elle indique que peons.exe a été installé, rappelle de relancer l'application pour utiliser la nouvelle version.
