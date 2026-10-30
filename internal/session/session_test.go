package session

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"peons/internal/mood"
)

var t0 = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

func at(s time.Duration) time.Time { return t0.Add(s) }

func ev(id, name string) Event { return Event{Session: id, Name: name} }

func TestTurnCycle(t *testing.T) {
	var s Store
	steps := []struct {
		event string
		after time.Duration
		want  mood.Snapshot
	}{
		{"SessionStart", 0, mood.Snapshot{Activity: mood.Idle, Since: at(0)}},
		{"UserPromptSubmit", time.Second, mood.Snapshot{Activity: mood.Working, Since: at(time.Second)}},
		// la suite du travail ne repousse pas le début de l'activité
		{"PreToolUse", 2 * time.Second, mood.Snapshot{Activity: mood.Working, Since: at(time.Second)}},
		{"PermissionRequest", 3 * time.Second, mood.Snapshot{Activity: mood.Awaiting, Since: at(3 * time.Second)}},
		{"PostToolUse", 4 * time.Second, mood.Snapshot{Activity: mood.Working, Since: at(4 * time.Second)}},
		{"SubagentStop", 5 * time.Second, mood.Snapshot{Activity: mood.Working, Since: at(4 * time.Second)}},
		{"PreCompact", 5500 * time.Millisecond, mood.Snapshot{Activity: mood.Compacting, Since: at(5500 * time.Millisecond)}},
		{"Stop", 6 * time.Second, mood.Snapshot{Activity: mood.Idle, Since: at(6 * time.Second), Answered: true}},
		{"StopFailure", 7 * time.Second, mood.Snapshot{Activity: mood.Idle, Since: at(7 * time.Second)}},
	}
	for _, st := range steps {
		s.Apply(ev("a", st.event), at(st.after))
		if got := s.Sessions()[0].Snap; got != st.want {
			t.Errorf("%s : %+v, attendu %+v", st.event, got, st.want)
		}
	}
}

func TestToolAndRecentHistory(t *testing.T) {
	var s Store
	s.Apply(Event{Session: "a", Name: "UserPromptSubmit", Cwd: `E:\Dev\peons`}, at(0))
	sess := s.Sessions()[0]
	if sess.Project != "peons" || !sess.Opened.Equal(at(0)) {
		t.Errorf("projet %q, ouverte à %v", sess.Project, sess.Opened)
	}
	s.Apply(Event{Session: "a", Name: "PreToolUse", Tool: "Bash"}, at(time.Second))
	if sess.Tool != "Bash" {
		t.Errorf("outil %q, attendu Bash", sess.Tool)
	}
	s.Apply(Event{Session: "a", Name: "PostToolUse", Tool: "Bash"}, at(2*time.Second))
	if sess.Tool != "" {
		t.Errorf("l'outil doit s'effacer à la fin : %q", sess.Tool)
	}
	s.Apply(Event{Session: "a", Name: "PermissionRequest", Tool: "Edit"}, at(3*time.Second))
	if sess.Tool != "Edit" {
		t.Errorf("la permission nomme l'outil : %q", sess.Tool)
	}
	for i := 0; i < 20; i++ {
		s.Apply(ev("a", "PreToolUse"), at(time.Duration(4+i)*time.Second))
	}
	if len(sess.Recent) != MaxRecent {
		t.Fatalf("%d lignes d'historique, attendu %d", len(sess.Recent), MaxRecent)
	}
	s.Apply(ev("a", "Stop"), at(time.Minute))
	if got := sess.Recent[len(sess.Recent)-1]; got != "Claude attend" || sess.Tool != "" {
		t.Errorf("dernière ligne %q, outil %q", got, sess.Tool)
	}
}

func TestContextReadsTheTranscriptOnlyWhenItChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	write := func(tokens int) {
		line := fmt.Sprintf(`{"type":"assistant","message":{"model":"claude-opus-4-5","usage":{"input_tokens":%d,"output_tokens":10}}}`+"\n", tokens)
		if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var s Store
	s.Apply(Event{Session: "a", Name: "SessionStart"}, at(0))
	sess := s.Sessions()[0]
	if _, ok := sess.Context(); ok {
		t.Fatal("sans journal, le contexte est inconnu")
	}
	write(1000)
	s.Apply(Event{Session: "a", Name: "Stop", Transcript: path}, at(time.Second))
	u, ok := sess.Context()
	if !ok || u.Tokens != 1010 || u.Window != 200_000 {
		t.Fatalf("contexte %+v ok=%v", u, ok)
	}
	// journal inchangé : pas de nouvelle lecture (on le vérifie en le faisant mentir sans changer taille ni date)
	st, _ := os.Stat(path)
	line, _ := os.ReadFile(path)
	os.WriteFile(path, []byte(strings.Replace(string(line), "1000", "9999", 1)), 0o600)
	os.Chtimes(path, st.ModTime(), st.ModTime())
	if u, _ := sess.Context(); u.Tokens != 1010 {
		t.Errorf("le journal n'a changé ni de taille ni de date : pas de relecture attendue, %d tokens", u.Tokens)
	}
	write(150_000)
	if u, _ := sess.Context(); u.Tokens != 150_010 {
		t.Errorf("après changement du journal : %d tokens, attendu 150010", u.Tokens)
	}
	os.Remove(path)
	if u, ok := sess.Context(); !ok || u.Tokens != 150_010 {
		t.Errorf("journal absent : la dernière valeur est gardée, %+v ok=%v", u, ok)
	}
}

func TestTitleComesFromTheTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	os.WriteFile(path, []byte(`{"type":"ai-title","aiTitle":"Mon titre"}`+"\n"), 0o600)
	var s Store
	s.Apply(Event{Session: "a", Name: "SessionStart", Cwd: `E:\Dev`}, at(0))
	sess := s.Sessions()[0]
	if sess.Title() != "" {
		t.Error("sans journal, pas de titre")
	}
	s.Apply(Event{Session: "a", Name: "Stop", Transcript: path}, at(time.Second))
	if sess.Title() != "Mon titre" || sess.Project != "Dev" {
		t.Errorf("titre %q, dossier %q", sess.Title(), sess.Project)
	}
	if _, ok := sess.Context(); ok {
		t.Error("un journal sans message de Claude n'a pas de contexte")
	}
	os.WriteFile(path, []byte(`{"type":"agent-name","agentName":"Renommée"}`+"\n"), 0o600)
	if sess.Title() != "Renommée" {
		t.Errorf("après /rename : %q", sess.Title())
	}
}

// Deux hooks rapprochés sont deux processus : le plus ancien peut arriver en second.
func TestOlderEventsDoNotUndoNewerOnes(t *testing.T) {
	var s Store
	s.Apply(Event{Session: "a", Name: "UserPromptSubmit", At: 100}, at(0))
	s.Apply(Event{Session: "a", Name: "Stop", At: 300}, at(time.Millisecond))          // le plus récent arrive d'abord
	s.Apply(Event{Session: "a", Name: "PostToolUse", At: 200}, at(2*time.Millisecond)) // le retardataire
	sess := s.Sessions()[0]
	if sess.Snap.Activity != mood.Idle || !sess.Snap.Answered {
		t.Errorf("le Stop (At=300) doit l'emporter sur le PostToolUse retardataire (At=200) : %+v", sess.Snap)
	}
	// sans horodatage (0), l'ordre d'arrivée reste la règle
	s.Apply(Event{Session: "a", Name: "UserPromptSubmit"}, at(time.Second))
	if sess.Snap.Activity != mood.Working {
		t.Errorf("un événement sans horodatage s'applique à son arrivée : %+v", sess.Snap)
	}
}

func TestApplyReportsChanges(t *testing.T) {
	var s Store
	if !s.Apply(ev("a", "SessionStart"), at(0)) {
		t.Error("une nouvelle session est un changement")
	}
	s.Apply(ev("a", "UserPromptSubmit"), at(1))
	if s.Apply(ev("a", "PreToolUse"), at(2)) {
		t.Error("PreToolUse pendant le travail ne change rien à l'affichage")
	}
	if s.Apply(ev("", "Stop"), at(3)) {
		t.Error("un événement sans session est ignoré")
	}
	if !s.Apply(ev("a", "SessionEnd"), at(4)) || len(s.Sessions()) != 0 {
		t.Error("SessionEnd retire la session")
	}
	if s.Apply(ev("a", "SessionEnd"), at(5)) {
		t.Error("retirer une session absente n'est pas un changement")
	}
}

func TestSessionsKeepArrivalOrder(t *testing.T) {
	var s Store
	for _, id := range []string{"a", "b", "c"} {
		s.Apply(ev(id, "SessionStart"), at(0))
	}
	s.Apply(ev("a", "Stop"), at(time.Second))
	s.Apply(ev("b", "SessionEnd"), at(time.Second))
	var got string
	for _, sess := range s.Sessions() {
		got += sess.ID
	}
	if got != "ac" {
		t.Errorf("ordre %q, attendu ac", got)
	}
}

func TestStaleWorkBecomesIdle(t *testing.T) {
	var s Store
	s.Apply(ev("a", "UserPromptSubmit"), at(0))
	sess := s.Sessions()[0]
	if got := sess.Snapshot(at(StaleWork - time.Second)); got.Activity != mood.Working {
		t.Errorf("avant le délai : %+v", got)
	}
	want := mood.Snapshot{Activity: mood.Idle, Since: at(StaleWork)}
	if got := sess.Snapshot(at(StaleWork + time.Hour)); got != want {
		t.Errorf("après le délai : %+v, attendu %+v", got, want)
	}
	// Awaiting peut durer : l'utilisateur est peut-être parti déjeuner
	s.Apply(ev("a", "PermissionRequest"), at(time.Minute))
	if got := sess.Snapshot(at(5 * time.Hour)); got.Activity != mood.Awaiting {
		t.Errorf("Awaiting ne doit pas expirer : %+v", got)
	}
}

// Un processus Claude Code ne porte qu'une session : la nouvelle (/resume, /clear)
// remplace l'ancienne, même si son SessionEnd n'est pas encore arrivé.
func TestNewSessionReplacesPreviousOfSameProcess(t *testing.T) {
	var s Store
	s.Apply(Event{Session: "a", Name: "SessionStart", PID: 1, Started: 10}, at(0))
	s.Apply(Event{Session: "other", Name: "SessionStart", PID: 2, Started: 20}, at(0))
	s.Apply(Event{Session: "b", Name: "SessionStart", PID: 1, Started: 10}, at(time.Second))
	got := []string{}
	for _, sess := range s.Sessions() {
		got = append(got, sess.ID)
	}
	if !slices.Equal(got, []string{"other", "b"}) {
		t.Errorf("sessions %v, attendu [other b]", got)
	}
	if s.Apply(ev("a", "SessionEnd"), at(2*time.Second)) {
		t.Error("le SessionEnd tardif de l'ancienne ne change plus rien")
	}
	// même PID mais autre date de lancement : un autre processus, une session de plus
	s.Apply(Event{Session: "c", Name: "SessionStart", PID: 1, Started: 99}, at(3*time.Second))
	if len(s.Sessions()) != 3 {
		t.Errorf("%d sessions, attendu 3", len(s.Sessions()))
	}
}

func TestPruneDeadAndExpired(t *testing.T) {
	var s Store
	s.Apply(Event{Session: "alive", Name: "SessionStart", PID: 1, Started: 10}, at(0))
	s.Apply(Event{Session: "dead", Name: "SessionStart", PID: 2, Started: 20}, at(0))
	s.Apply(ev("nopid", "SessionStart"), at(0))
	alive := func(pid uint32, _ int64) bool { return pid == 1 }

	if !s.Prune(at(time.Second), alive) || len(s.Sessions()) != 2 {
		t.Fatalf("le processus mort doit partir : %d sessions", len(s.Sessions()))
	}
	// la vérification est espacée : une seconde plus tard, pas de nouvel appel
	calls := 0
	s.Prune(at(2*time.Second), func(uint32, int64) bool { calls++; return true })
	if calls != 0 {
		t.Errorf("%d vérifications avant LivenessEvery", calls)
	}
	s.Prune(at(time.Second+LivenessEvery), func(uint32, int64) bool { calls++; return true })
	if calls != 1 {
		t.Errorf("%d vérifications, attendu 1 (une seule session a un PID)", calls)
	}
	s.Apply(ev("alive", "Stop"), at(ExpireAfter-time.Minute)) // celle-ci reste active
	if !s.Prune(at(ExpireAfter), alive) {
		t.Error("la session sans PID et sans nouvelle doit expirer")
	}
	if got := s.Sessions(); len(got) != 1 || got[0].ID != "alive" {
		t.Errorf("sessions restantes : %v", got)
	}
}

func TestStoreIsBounded(t *testing.T) {
	var s Store
	for i := 0; i < 10*MaxSessions; i++ {
		s.Apply(ev(fmt.Sprint("s", i), "SessionStart"), at(time.Duration(i)*time.Second))
		if len(s.Sessions()) > MaxSessions {
			t.Fatalf("%d sessions", len(s.Sessions()))
		}
	}
	// la plus silencieuse part, les récentes restent
	if s.find("s0") != nil || s.find(fmt.Sprint("s", 10*MaxSessions-1)) == nil {
		t.Error("l'éviction doit viser la plus ancienne")
	}
}

func TestNextCheck(t *testing.T) {
	var s Store
	if !s.NextCheck(at(0)).IsZero() {
		t.Error("rien à surveiller sans session")
	}
	s.Apply(ev("a", "UserPromptSubmit"), at(0))
	if got := s.NextCheck(at(time.Second)); !got.Equal(at(StaleWork)) {
		t.Errorf("prochain contrôle %v, attendu la fin du travail à %v", got, at(StaleWork))
	}
	s.Apply(Event{Session: "a", Name: "Stop", PID: 5}, at(time.Second))
	s.Prune(at(time.Second), func(uint32, int64) bool { return true })
	if got := s.NextCheck(at(2 * time.Second)); !got.Equal(at(time.Second + LivenessEvery)) {
		t.Errorf("prochain contrôle %v, attendu la prochaine vérification du PID", got)
	}
}

// Annuler un tour n'envoie aucun hook (Échap) : le minage doit s'arrêter dès que le
// journal montre l'annulation et qu'un court silence (grâce) a passé — bien avant les
// 15 min de StaleWork. Un nouveau prompt, qui lui envoie un hook, reprend la main.
func TestInterruptedWorkBecomesIdle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	write := func(lines ...string) {
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	asst := `{"type":"assistant","message":{"model":"claude-opus-4-5","usage":{"input_tokens":1000,"output_tokens":10}}}`
	interrupt := `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}`

	var s Store
	s.Apply(Event{Session: "a", Name: "PreToolUse", Tool: "Bash", Transcript: path}, at(0))
	sess := s.Sessions()[0]

	write(asst) // Claude parle : on mine
	if got := sess.Snapshot(at(time.Second)).Activity; got != mood.Working {
		t.Fatalf("journal normal : %v, attendu Working", got)
	}
	write(asst, interrupt) // l'utilisateur annule
	if got := sess.Snapshot(at(InterruptGrace - time.Second)).Activity; got != mood.Working {
		t.Errorf("dans la grâce : %v, attendu Working (laisser un prompt reprendre)", got)
	}
	if got := sess.Snapshot(at(InterruptGrace + 2*time.Second)).Activity; got != mood.Idle {
		t.Errorf("après l'annulation : %v, attendu Idle bien avant StaleWork", got)
	}
	// un nouveau prompt reprend le minage malgré le marqueur encore présent dans le journal
	s.Apply(Event{Session: "a", Name: "UserPromptSubmit", Transcript: path}, at(time.Minute))
	if got := sess.Snapshot(at(time.Minute)).Activity; got != mood.Working {
		t.Errorf("après un nouveau prompt : %v, attendu Working", got)
	}
}

// agentEv forge un événement de sous-agent (il porte un agent_id, en plus du
// session_id du parent).
func agentEv(sid, aid, name string) Event {
	return Event{Session: sid, Name: name, Agent: aid, AgentType: "general-purpose"}
}

// Un événement de sous-agent ne touche pas le corps du parent : il porte le
// session_id du parent mais ne fait qu'alimenter le suivi des sous-agents, qui sont
// symbolisés en particules au-dessus du clawd, jamais par son état à lui.
func TestSubagentEventsDoNotDriveParentBody(t *testing.T) {
	var s Store
	s.Apply(ev("a", "SessionStart"), at(0))
	s.Apply(ev("a", "Stop"), at(time.Second)) // le parent a fini son tour : Idle
	sess := s.Sessions()[0]
	if s.Apply(agentEv("a", "ag1", "PreToolUse"), at(2*time.Second)) {
		t.Error("un événement de sous-agent ne change pas la liste des sessions")
	}
	if got := sess.Snapshot(at(2 * time.Second)); got.Activity != mood.Idle {
		t.Errorf("le corps du parent ne doit pas suivre un sous-agent : %+v", got)
	}
	// le parent étant inactif, son sous-agent vivant est « attendu » (throttle)
	st := sess.AgentStates(at(2*time.Second), true)
	if len(st) != 1 || st[0].ID != "ag1" || st[0].Kind != AgentThrottle {
		t.Errorf("états des agents : %+v, attendu [ag1 throttle]", st)
	}
}

func TestAgentStatesResolution(t *testing.T) {
	var s Store
	s.Apply(ev("a", "UserPromptSubmit"), at(0)) // parent au travail
	sess := s.Sessions()[0]
	s.Apply(agentEv("a", "ag1", "PreToolUse"), at(time.Second))  // ag1 : outil en cours
	s.Apply(agentEv("a", "ag2", "PreToolUse"), at(time.Second))  // ag2 : outil…
	s.Apply(agentEv("a", "ag2", "PostToolUse"), at(time.Second)) // …puis fini

	// parent au travail, tout de suite : les deux sont actifs (working)
	if st := sess.AgentStates(at(2*time.Second), false); len(st) != 2 ||
		st[0].Kind != AgentWorking || st[1].Kind != AgentWorking {
		t.Fatalf("états immédiats : %+v, attendu [working working]", st)
	}
	// bien plus tard : ag1 a toujours un outil en cours (working), ag2 est muet (idle)
	late := at(time.Second + AgentActive + time.Second)
	byID := map[string]AgentKind{}
	for _, a := range sess.AgentStates(late, false) {
		byID[a.ID] = a.Kind
	}
	if byID["ag1"] != AgentWorking {
		t.Errorf("ag1 (outil en cours) doit rester working : %v", byID["ag1"])
	}
	if byID["ag2"] != AgentIdle {
		t.Errorf("ag2 (muet depuis AgentActive) doit être idle : %v", byID["ag2"])
	}
	// parent inactif : tous ses agents vivants passent throttle
	for _, a := range sess.AgentStates(late, true) {
		if a.Kind != AgentThrottle {
			t.Errorf("parent inactif : %s attendu throttle, %v", a.ID, a.Kind)
		}
	}
}

func TestSubagentStopRemovesAndStaleExpires(t *testing.T) {
	var s Store
	s.Apply(ev("a", "UserPromptSubmit"), at(0))
	sess := s.Sessions()[0]
	s.Apply(agentEv("a", "ag1", "PreToolUse"), at(time.Second))
	s.Apply(agentEv("a", "ag2", "PostToolUse"), at(time.Second)) // pas d'outil en cours
	if len(sess.AgentStates(at(time.Second), false)) != 2 {
		t.Fatal("ag1 et ag2 devraient être suivis")
	}
	s.Apply(agentEv("a", "ag1", "SubagentStop"), at(2*time.Second)) // ag1 fini
	st := sess.AgentStates(at(2*time.Second), false)
	if len(st) != 1 || st[0].ID != "ag2" {
		t.Errorf("après SubagentStop d'ag1 : %+v, attendu [ag2]", st)
	}
	// ag2 n'a jamais de SubagentStop (session tuée) : oublié après AgentExpire
	if got := sess.AgentStates(at(time.Second+AgentExpire), false); len(got) != 0 {
		t.Errorf("un agent muet depuis AgentExpire doit être oublié : %+v", got)
	}
}

func TestAgentOrderIsAppearance(t *testing.T) {
	var s Store
	s.Apply(ev("a", "UserPromptSubmit"), at(0))
	sess := s.Sessions()[0]
	for _, id := range []string{"z", "m", "a"} { // ordre d'apparition, pas alphabétique
		s.Apply(agentEv("a", id, "PreToolUse"), at(time.Second))
	}
	var got string
	for _, a := range sess.AgentStates(at(time.Second), false) {
		got += a.ID
	}
	if got != "zma" {
		t.Errorf("ordre %q, attendu zma (ordre d'apparition)", got)
	}
}
