---
description: Termine la fiche de reprise de la session courante (le sujet est fini, inutile de la retrouver après un plantage)
disable-model-invocation: true
allowed-tools: Bash("$LOCALAPPDATA/peons/peons.exe" done*)
---

!`"$LOCALAPPDATA/peons/peons.exe" done ${CLAUDE_SESSION_ID}`

Confirme en une ligne que la fiche est terminée.
