package main

import (
	"encoding/json"
	"io"
	"os"
	"time"

	"peons/internal/ipc"
	"peons/internal/proc"
	"peons/internal/resume"
	"peons/internal/session"
)

// capturesProcess : les événements où le hook cherche le processus Claude Code.
var capturesProcess = map[string]bool{"SessionStart": true, "UserPromptSubmit": true, "Stop": true, "PermissionRequest": true, "PreCompact": true}

// Le temps laissé à une application qu'on vient de lancer pour ouvrir sa socket.
const startWait = 3 * time.Second

// runHook : Claude Code lance ce programme à chaque événement, le JSON de
// l'événement sur stdin. Il n'écrit jamais sur stdout et sort toujours en 0
// pour ne pas influencer les décisions de Claude Code.
func runHook() {
	var in struct {
		Session string `json:"session_id"`
		Cwd     string `json:"cwd"`
		Name    string `json:"hook_event_name"`
		Tool    string `json:"tool_name"`
		Journal string `json:"transcript_path"`
		// Un événement issu d'un sous-agent (outil Agent/Task) porte le session_id du
		// PARENT mais, en plus, l'agent_id/agent_type du sous-agent (vérifié au runtime :
		// PreToolUse, PostToolUse et SubagentStop les portent). Vide pour le fil principal.
		AgentID   string `json:"agent_id"`
		AgentType string `json:"agent_type"`
	}
	// la charge utile peut être grosse (sorties d'outils) : on lit au plus 8 Mo
	if json.NewDecoder(io.LimitReader(os.Stdin, 8<<20)).Decode(&in) != nil {
		return
	}
	ev := session.Event{Session: in.Session, Name: in.Name, Cwd: in.Cwd, Tool: in.Tool, Transcript: in.Journal, Agent: in.AgentID, AgentType: in.AgentType, At: time.Now().UnixNano()}
	// le processus Claude Code permet à l'app de voir un terminal fermé
	// (aucun SessionEnd n'est envoyé dans ce cas), et de retrouver son terminal au clic.
	// Trouver ce processus coûte une vingtaine de ms : on ne le fait qu'aux événements
	// rares, pas à chaque outil. Stop, PermissionRequest et PreCompact rattrapent une
	// session déjà en cours quand l'application démarre.
	if capturesProcess[in.Name] {
		if pid, started, ok := proc.FindClaude(); ok {
			ev.PID, ev.Started = pid, started
		}
	}
	// la fiche de reprise est écrite ici, pas par l'application : elle doit exister
	// même si l'application ne tourne pas, et survivre à un plantage
	if in.Name == "SessionStart" {
		if err := resume.Start(resume.Entry{ID: in.Session, Cwd: in.Cwd, Transcript: in.Journal, PID: ev.PID, Started: ev.Started}); err != nil {
			logf("hook : fiche de reprise : %v", err)
		}
	}
	if ipc.Send(ev) == nil || in.Name == "SessionEnd" {
		return // inutile de lancer l'app juste pour apprendre une fin de session
	}
	if err := startDetached(); err != nil {
		logf("hook : lancement de l'application impossible : %v", err)
		return
	}
	for deadline := time.Now().Add(startWait); time.Now().Before(deadline); time.Sleep(40 * time.Millisecond) {
		if ipc.Send(ev) == nil {
			return
		}
	}
}
