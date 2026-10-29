---
description: Liste les sessions Claude Code à reprendre (interrompues par un plantage ou un terminal fermé) avec la commande de reprise
disable-model-invocation: true
allowed-tools: Bash("$LOCALAPPDATA/peons/peons.exe" sessions*)
---

Sessions connues de peons :

!`"$LOCALAPPDATA/peons/peons.exe" sessions`

Recopie cette liste telle quelle. Pour chaque session « interrompue », la ligne suivante est la commande qui la reprend : ne la lance pas, l'utilisateur la colle dans un nouveau terminal.
