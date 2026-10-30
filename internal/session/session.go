// Package session suit les sessions Claude Code à partir des événements des
// hooks et dit, pour chacune, ce qu'elle fait (mood.Snapshot).
//
// Le Store n'est pas protégé par un verrou : il appartient à la boucle
// d'affichage, qui est seule à l'appeler. Les événements lui arrivent par un
// canal. Sa taille est bornée (MaxSessions) et chaque session finit par
// disparaître (SessionEnd, processus mort, inactivité), donc il ne peut pas
// grossir indéfiniment.
package session

import (
	"os"
	"path/filepath"
	"slices"
	"time"

	"peons/internal/mood"
	"peons/internal/transcript"
)

const (
	// MaxSessions borne le nombre de sessions suivies ; au-delà, la plus
	// silencieuse est oubliée.
	MaxSessions = 32
	// ExpireAfter : une session sans aucun événement depuis ce délai est
	// considérée comme partie (terminal fermé sans SessionEnd, PID inconnu).
	ExpireAfter = 2 * time.Hour
	// StaleWork : Claude « travaille » depuis ce délai sans le moindre événement.
	// Un tour interrompu avec Échap n'envoie pas de Stop ; sans cette limite, le
	// bonhomme resterait éveillé pour toujours.
	StaleWork = 15 * time.Minute
	// LivenessEvery : cadence de vérification des processus Claude Code.
	LivenessEvery = 3 * time.Second
	// InterruptPoll : pendant que Claude travaille, cadence de relecture du journal
	// pour repérer une annulation (Échap n'envoie aucun hook).
	InterruptPoll = time.Second
	// InterruptGrace : un marqueur d'annulation dans le journal n'arrête le minage
	// qu'après ce silence sans hook — le temps qu'un nouveau prompt, qui lui envoie
	// un hook, reprenne la main sans clignotement.
	InterruptGrace = 5 * time.Second
	// AgentActive : au-delà de ce silence (et hors outil en cours), un sous-agent est
	// montré inactif plutôt qu'au travail.
	AgentActive = 10 * time.Second
	// AgentExpire : un sous-agent sans le moindre événement depuis ce délai est oublié —
	// filet de sécurité si son SubagentStop n'arrive jamais (session tuée).
	AgentExpire = 2 * time.Minute
)

// AgentKind est l'état d'un sous-agent, tel qu'il est symbolisé au-dessus du clawd.
type AgentKind int

const (
	AgentIdle     AgentKind = iota // vivant, sans activité récente (inactif ou en attente)
	AgentWorking                   // en train d'utiliser des outils
	AgentThrottle                  // la session principale a fini son tour et l'attend
)

// AgentState est un sous-agent vivant et son état courant.
type AgentState struct {
	ID   string
	Kind AgentKind
}

// agentInfo suit un sous-agent vivant d'une session.
type agentInfo struct {
	last   time.Time // dernier événement reçu de ce sous-agent
	inTool bool      // un outil est en cours (PreToolUse vu, pas encore son PostToolUse)
	seq    int       // ordre d'apparition, pour un affichage stable
	typ    string    // agent_type
}

// Event est ce qu'un hook envoie à l'application : le strict nécessaire
// (ni prompt, ni sortie d'outil), la socket n'a pas à transporter plus.
type Event struct {
	Session    string `json:"session"`              // session_id de Claude Code
	Name       string `json:"event"`                // nom du hook (Stop, PreToolUse…)
	Cwd        string `json:"cwd,omitempty"`        // dossier du projet
	Tool       string `json:"tool,omitempty"`       // outil concerné (PreToolUse, PermissionRequest…)
	Transcript string `json:"transcript,omitempty"` // journal de la session (JSONL), d'où l'on estime le contexte
	// Agent : l'agent_id d'un sous-agent (outil Agent/Task). Un tel événement porte le
	// session_id du PARENT, plus cet agent_id ; vide pour le fil principal. Il sert à
	// symboliser les sous-agents au-dessus du clawd parent, sans toucher à son corps.
	Agent     string `json:"agent,omitempty"`
	AgentType string `json:"agent_type,omitempty"` // type du sous-agent (general-purpose…)
	// At : l'instant de l'événement chez le hook (UnixNano). Les hooks sont des
	// processus distincts : deux événements rapprochés peuvent arriver dans le désordre.
	At      int64  `json:"at,omitempty"`
	PID     uint32 `json:"pid,omitempty"`     // processus Claude Code, pour détecter un terminal fermé
	Started int64  `json:"started,omitempty"` // sa date de création, contre la réutilisation du PID
}

// MaxRecent borne l'historique d'événements gardé par session.
const MaxRecent = 6

// Session est l'état suivi d'une session.
type Session struct {
	ID          string
	Project     string
	Snap        mood.Snapshot
	Opened      time.Time        // première apparition
	Last        time.Time        // dernier événement reçu
	Tool        string           // outil en cours, vide hors d'un outil
	Transcript  string           // journal de la session, vide s'il n'est pas connu
	lastAt      int64            // instant (chez le hook) du dernier événement appliqué
	usage       transcript.Usage // dernier usage lu dans le journal
	title       string           // dernier titre lu dans le journal
	usageOK     bool
	usageSize   int64 // taille et date du journal au moment de la lecture
	usageMod    time.Time
	interrupted bool      // le journal se termine sur une annulation (voir Snapshot)
	lastPoll    time.Time // dernière relecture du journal pour chercher une annulation
	Recent      []string  // derniers événements, du plus ancien au plus récent
	PID         uint32
	Started     int64
	agents      map[string]*agentInfo // sous-agents vivants, par agent_id
	agentSeq    int                   // compteur d'apparition des sous-agents
}

// Store contient les sessions, dans l'ordre de leur première apparition (c'est
// l'ordre d'affichage).
type Store struct {
	sessions []*Session
	nextLive time.Time
}

// Sessions donne les sessions dans l'ordre d'affichage. Le slice appartient au Store.
func (s *Store) Sessions() []*Session { return s.sessions }

// Apply prend en compte un événement et dit si l'ensemble des sessions a changé
// (arrivée, départ ou nouvelle activité).
func (s *Store) Apply(ev Event, now time.Time) bool {
	if ev.Session == "" {
		return false
	}
	if ev.Name == "SessionEnd" {
		return s.remove(ev.Session)
	}
	sess, created := s.find(ev.Session), false
	if sess == nil {
		sess, created = s.add(ev.Session, now), true
	}
	if ev.Agent != "" {
		// Événement d'un sous-agent : il porte le session_id du parent mais ne doit
		// changer ni le corps du parent ni son horloge d'inactivité (l'activité d'un
		// agent de fond masquerait une annulation du parent). Seul le suivi des
		// sous-agents est mis à jour ; ils sont montrés en particules au-dessus du clawd.
		sess.trackAgent(ev, now)
		return created
	}
	sess.Last = now
	if ev.Cwd != "" {
		sess.Project = filepath.Base(ev.Cwd)
	}
	if ev.PID != 0 {
		sess.PID, sess.Started = ev.PID, ev.Started
		if created {
			s.dropPredecessors(sess)
		}
	}
	if ev.Transcript != "" {
		sess.Transcript = ev.Transcript
	}
	// Un événement plus ancien que le dernier appliqué ne doit pas défaire l'état :
	// un Stop traité avant son PostToolUse laisserait le bonhomme à creuser pour
	// toujours.
	if ev.At != 0 {
		if ev.At < sess.lastAt {
			return created
		}
		sess.lastAt = ev.At
	}
	// un hook, quel qu'il soit, est une activité plus récente qu'une éventuelle
	// annulation déjà vue : on oublie celle-ci, la relecture du journal la redétectera
	// si elle tient toujours.
	sess.interrupted, sess.lastPoll = false, time.Time{}
	before := sess.Snap
	switch ev.Name {
	case "SessionStart", "StopFailure":
		// démarrage, ou tour fini sur une erreur : rien à fêter
		sess.Snap = mood.Snapshot{Activity: mood.Idle, Since: now}
		sess.Tool = ""
	case "Stop":
		sess.Snap = mood.Snapshot{Activity: mood.Idle, Since: now, Answered: true}
		sess.Tool = ""
	case "PermissionRequest":
		sess.Snap = mood.Snapshot{Activity: mood.Awaiting, Since: now}
		sess.Tool = ev.Tool
	case "PreCompact":
		sess.Snap = mood.Snapshot{Activity: mood.Compacting, Since: now}
		sess.Tool = ""
	case "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure", "PermissionDenied":
		if sess.Snap.Activity != mood.Working {
			sess.Snap = mood.Snapshot{Activity: mood.Working, Since: now}
		}
		sess.Tool = ""
		if ev.Name == "PreToolUse" {
			sess.Tool = ev.Tool
		}
	}
	sess.Recent = append(sess.Recent, Describe(ev.Name, ev.Tool))
	if len(sess.Recent) > MaxRecent {
		sess.Recent = slices.Delete(sess.Recent, 0, len(sess.Recent)-MaxRecent)
	}
	// SubagentStop et les événements inconnus ne changent pas l'activité : un
	// sous-agent qui termine ne veut pas dire que Claude a répondu.
	return created || sess.Snap != before
}

// Describe donne la ligne d'historique d'un événement.
func Describe(event, tool string) string {
	if tool == "" {
		tool = "outil"
	}
	switch event {
	case "UserPromptSubmit":
		return "Prompt envoyé"
	case "SessionStart":
		return "Session démarrée"
	case "PreToolUse":
		return "Lance " + tool
	case "PostToolUse":
		return tool + " terminé"
	case "PostToolUseFailure":
		return tool + " a échoué"
	case "PermissionRequest":
		return "Permission demandée pour " + tool
	case "PermissionDenied":
		return tool + " refusé (mode auto)"
	case "StopFailure":
		return "Tour terminé sur une erreur d'API"
	case "PreCompact":
		return "Compactage du contexte"
	case "Stop":
		return "Claude attend"
	case "SubagentStop":
		return "Sous-agent terminé"
	}
	return event
}

// busy : Claude travaille ou compacte, états qui ne durent que le temps d'une tâche.
func (sess *Session) busy() bool {
	return sess.Snap.Activity == mood.Working || sess.Snap.Activity == mood.Compacting
}

// Snapshot donne l'activité de la session à l'instant now. Pendant que Claude
// travaille, il tient compte de deux cas où aucun hook ne vient : une annulation
// (Échap, repérée dans le journal) et un travail resté muet trop longtemps (StaleWork).
func (sess *Session) Snapshot(now time.Time) mood.Snapshot {
	if sess.busy() {
		sess.pollInterrupt(now)
		quiet := now.Sub(sess.Last)
		if sess.interrupted && quiet >= InterruptGrace {
			return mood.Snapshot{Activity: mood.Idle, Since: sess.Last}
		}
		if quiet >= StaleWork {
			return mood.Snapshot{Activity: mood.Idle, Since: sess.Last.Add(StaleWork)}
		}
	}
	return sess.Snap
}

// pollInterrupt relit le journal, au plus une fois par InterruptPoll, pour savoir
// s'il se termine sur une annulation (aucun hook ne la signale).
func (sess *Session) pollInterrupt(now time.Time) {
	if sess.Transcript == "" || now.Sub(sess.lastPoll) < InterruptPoll {
		return
	}
	sess.lastPoll = now
	sess.refresh()
}

// trackAgent met à jour le suivi d'un sous-agent depuis un événement qui porte son
// agent_id (ev.Agent non vide).
func (sess *Session) trackAgent(ev Event, now time.Time) {
	if ev.Name == "SubagentStop" { // le sous-agent a fini : il s'efface
		delete(sess.agents, ev.Agent)
		return
	}
	if sess.agents == nil {
		sess.agents = map[string]*agentInfo{}
	}
	a := sess.agents[ev.Agent]
	if a == nil {
		a = &agentInfo{seq: sess.agentSeq, typ: ev.AgentType}
		sess.agentSeq++
		sess.agents[ev.Agent] = a
	}
	a.last = now
	switch ev.Name {
	case "PreToolUse":
		a.inTool = true // un outil tourne : au travail même sans autre événement d'ici sa fin
	case "PostToolUse", "PostToolUseFailure", "PermissionDenied":
		a.inTool = false
	}
}

// AgentStates donne les sous-agents vivants de la session et leur état, du plus
// ancien au plus récent. parentIdle : la session principale n'a plus d'activité
// propre (elle attend) — ses sous-agents sont alors « attendus » (throttle). Les
// sous-agents muets depuis trop longtemps (SubagentStop manqué) sont oubliés ici.
func (sess *Session) AgentStates(now time.Time, parentIdle bool) []AgentState {
	if len(sess.agents) == 0 {
		return nil
	}
	out := make([]AgentState, 0, len(sess.agents))
	for id, a := range sess.agents {
		if now.Sub(a.last) >= AgentExpire {
			delete(sess.agents, id)
			continue
		}
		kind := AgentIdle
		switch {
		case parentIdle:
			kind = AgentThrottle
		case a.inTool || now.Sub(a.last) < AgentActive:
			kind = AgentWorking
		}
		out = append(out, AgentState{ID: id, Kind: kind})
	}
	slices.SortFunc(out, func(x, y AgentState) int {
		return sess.agents[x.ID].seq - sess.agents[y.ID].seq
	})
	return out
}

// Context donne le remplissage du contexte de la session, lu dans son journal.
// Le journal n'est relu que s'il a changé (taille ou date) depuis la dernière fois :
// consulter le contexte à chaque rafraîchissement d'une étiquette ne coûte qu'un stat.
func (sess *Session) Context() (transcript.Usage, bool) {
	sess.refresh()
	return sess.usage, sess.usageOK
}

// Title donne le titre de la session, celui que Claude Code montre dans l'onglet
// du terminal (le nom donné par /rename, sinon le titre généré), lu dans son
// journal ; vide tant qu'elle n'en a pas.
func (sess *Session) Title() string {
	sess.refresh()
	return sess.title
}

// refresh relit le journal s'il a changé depuis la dernière lecture.
func (sess *Session) refresh() {
	if sess.Transcript == "" {
		return
	}
	st, err := os.Stat(sess.Transcript)
	if err != nil {
		return // journal momentanément absent : on garde ce qu'on avait
	}
	if sess.usageSize == st.Size() && sess.usageMod.Equal(st.ModTime()) {
		return
	}
	sess.usageSize, sess.usageMod = st.Size(), st.ModTime()
	info, _ := transcript.Scan(sess.Transcript)
	sess.interrupted = info.Interrupted
	if info.HasUsage {
		sess.usage, sess.usageOK = info.Usage, true
	}
	if info.Title != "" {
		sess.title = info.Title
	}
}

// Prune retire les sessions parties : inactives depuis ExpireAfter, ou dont le
// processus Claude Code est mort (alive est interrogé au plus toutes les
// LivenessEvery). Renvoie vrai si l'ensemble a changé.
func (s *Store) Prune(now time.Time, alive func(pid uint32, started int64) bool) bool {
	check := !now.Before(s.nextLive)
	if check {
		s.nextLive = now.Add(LivenessEvery)
	}
	kept, changed := s.sessions[:0], false
	for _, sess := range s.sessions {
		gone := now.Sub(sess.Last) >= ExpireAfter ||
			(check && sess.PID != 0 && alive != nil && !alive(sess.PID, sess.Started))
		if gone {
			changed = true
			continue
		}
		kept = append(kept, sess)
	}
	clear(s.sessions[len(kept):]) // lâche les sessions retirées pour le GC
	s.sessions = kept
	return changed
}

// NextCheck donne le prochain instant où Prune ou Snapshot peuvent changer
// quelque chose sans nouvel événement, ou le zéro de time.Time s'il n'y a rien à
// surveiller.
func (s *Store) NextCheck(now time.Time) time.Time {
	var next time.Time
	soon := func(t time.Time) {
		if next.IsZero() || t.Before(next) {
			next = t
		}
	}
	for _, sess := range s.sessions {
		soon(sess.Last.Add(ExpireAfter))
		if sess.busy() && now.Sub(sess.Last) < StaleWork {
			soon(sess.Last.Add(StaleWork))
		}
		// relecture du journal tant qu'on travaille, pour attraper une annulation ;
		// inutile une fois celle-ci confirmée (après la grâce)
		if sess.busy() && sess.Transcript != "" && !(sess.interrupted && now.Sub(sess.Last) >= InterruptGrace) {
			soon(sess.lastPoll.Add(InterruptPoll))
			if sess.interrupted {
				soon(sess.Last.Add(InterruptGrace))
			}
		}
		if sess.PID != 0 {
			soon(s.nextLive)
		}
		if sess.PID != 0 && s.nextLive.IsZero() {
			soon(now) // jamais vérifiée : tout de suite
		}
	}
	return next
}

func (s *Store) find(id string) *Session {
	for _, sess := range s.sessions {
		if sess.ID == id {
			return sess
		}
	}
	return nil
}

func (s *Store) add(id string, now time.Time) *Session {
	if len(s.sessions) >= MaxSessions {
		oldest := 0
		for i, sess := range s.sessions {
			if sess.Last.Before(s.sessions[oldest].Last) {
				oldest = i
			}
		}
		s.sessions = slices.Delete(s.sessions, oldest, oldest+1)
	}
	sess := &Session{ID: id, Opened: now, Last: now, Snap: mood.Snapshot{Activity: mood.Idle, Since: now}}
	s.sessions = append(s.sessions, sess)
	return sess
}

// SameProcess dit si les deux sessions tournent dans le même processus Claude Code :
// /resume et /clear y ouvrent une nouvelle session, qui prend la suite de l'ancienne.
func (sess *Session) SameProcess(o *Session) bool {
	return sess.PID != 0 && sess.PID == o.PID && sess.Started == o.Started
}

// dropPredecessors retire les sessions que sess remplace : un processus n'en porte
// qu'une à la fois, et le SessionEnd de l'ancienne peut arriver après le
// SessionStart de la nouvelle.
func (s *Store) dropPredecessors(sess *Session) {
	s.sessions = slices.DeleteFunc(s.sessions, func(o *Session) bool { return o != sess && o.SameProcess(sess) })
}

func (s *Store) remove(id string) bool {
	for i, sess := range s.sessions {
		if sess.ID == id {
			s.sessions = slices.Delete(s.sessions, i, i+1)
			return true
		}
	}
	return false
}
