package main

import (
	"flag"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/draw"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"peons/assets"
	"peons/internal/avatar"
	"peons/internal/ipc"
	"peons/internal/mood"
	"peons/internal/prefs"
	"peons/internal/proc"
	"peons/internal/session"
	"peons/internal/snap"
	"peons/internal/terrain"
)

var opt struct {
	ui      int
	happy   time.Duration
	neutral time.Duration
	sad     time.Duration
	work    time.Duration
	secs    int
	tray    bool
	trace   bool
	stats   time.Duration
	demo    bool
	// background : lancé par le hook, pour suivre les sessions ; sans cela
	// (double-clic), les réglages s'ouvrent.
	background bool
	pin        string
	dx         int // décalage horizontal de la fenêtre, pour montrer plusieurs copies côte à côte
}

const (
	islandPad   = 4  // pixels d'art entre le bord de la fenêtre et ce qu'elle contient
	headroom    = 8  // au-dessus du corps, pour la bulle
	maxSections = 60 // sections centrales de l'île au plus
	maxShown    = 8  // bonhommes affichés ; les plus anciens cèdent la place
	topGap      = 30 // distance au haut de l'écran à la première utilisation

	// Échelle : pixels d'écran par pixel de sprite (1 = un pour un).
	defaultScale = 3
	minScale     = 1
	maxScale     = 8

	// Taille de l'étiquette de survol, indépendante de celle des clawd : multiple de
	// l'échelle de l'écran.
	defaultTipSize = 1
	minTipSize     = 1
	maxTipSize     = 4

	// trimAfter : calme après lequel le tas est rendu au système d'exploitation.
	trimAfter = 20 * time.Second
	// settingsTick : la fenêtre de réglages ouverte se rafraîchit à cette cadence (durées).
	settingsTick = time.Second
	// dustFrame : cadence de la désagrégation d'un bonhomme qui s'en va.
	dustFrame = 40 * time.Millisecond

	// Apparition : un portail ovale s'ouvre et le clawd en tombe, tout droit, neutre.
	portalFrame = 40 * time.Millisecond
	portalDur   = 900 * time.Millisecond // durée totale de l'apparition
	portalOpen  = 280 * time.Millisecond // fin de l'ouverture du portail
	portalShut  = 680 * time.Millisecond // début de sa fermeture
	fallStart   = 200 * time.Millisecond // le clawd commence à tomber
	fallEnd     = 650 * time.Millisecond // il est posé
)

// view est un bonhomme à l'écran : sa session et ce qui est dessiné pour elle.
type view struct {
	sess      *session.Session
	look      mood.Look
	animStart time.Time   // démarrage de l'animation de l'aspect courant : sa seule horloge
	pose      avatar.Pose // frames à afficher maintenant

	drawn    bool // faux tant que rien n'a été dessiné pour cette vue
	drawLook mood.Look
	drawPose avatar.Pose

	// particles : les particules des sous-agents, au-dessus de la tête, recalculées
	// chaque frame (position + flottement) ; partSig détecte un changement à dessiner.
	particles []placed
	partSig   string

	// dying : l'instant où sa session est partie. Figé dans sa dernière pose, il se
	// désagrège (dust) avant de quitter sa place. Zéro : il est là.
	dying   time.Time
	dust    *snap.Snap    // nil : rien à désagréger, il part aussitôt
	dustAt  time.Duration // où en est la désagrégation, au dernier update
	dustBuf *image.NRGBA  // où elle est dessinée : la fenêtre, en pixels d'art

	// born : l'instant où sa session est apparue. Un portail s'ouvre et le clawd en
	// tombe, en position neutre, avant de prendre sa place. Zéro : il est installé.
	born   time.Time
	bornAt time.Duration // où en est l'apparition, au dernier update
}

type app struct {
	char     *avatar.Character
	agentFX  map[session.AgentKind]*image.NRGBA // sprites des particules de sous-agents
	portalFX *image.NRGBA                       // texture du portail d'apparition
	cfg      mood.Config
	ov       overlay          // la fenêtre transparente (superposée native)
	terr     *terrain.Terrain // les morceaux de l'île
	crowd    crowd            // l'écart entre les bonhommes et ce que chacun dessine
	rng      *rand.Rand
	picks    []int // variante de chaque section centrale : tirée une fois, pour que l'île ne change pas d'aspect quand elle grandit

	store    session.Store
	views    []*view
	events   chan session.Event
	selected string // session choisie par un clic ; vide : la plus récente
	prefs    prefs.Prefs
	set      *settings // fenêtre de réglages, nil quand elle est fermée
	tray     *tray     // l'icône de la zone de notification, nil sans elle

	n            int // nombre d'emplacements pour lesquels la fenêtre est dimensionnée
	slotsX0      int // abscisse (pixels d'art) du premier emplacement
	below        int // pixels d'art sous la ligne des pieds
	W, H, ground int // taille de la fenêtre et ligne des pieds, en pixels écran
	centerX, top int // position : milieu du bord haut, pour grandir autour du même axe
	ui           int // échelle des sprites, du socle d'herbe et de la fenêtre
	tipSize      int // taille de l'étiquette de survol (1 à maxTipSize), à part de ui
	shown        bool
	appeared     bool // vrai après le premier peuplement : les clawd suivants arrivent par un portail
	drag         struct {
		on, moved bool
		win       image.Point // position de la fenêtre au début du glissement
		mouse     [2]float32  // position globale de la souris au début
	}

	dirty   bool
	tip     tooltip   // l'étiquette de survol
	tipView *view     // le bonhomme pointé, nil si l'étiquette est cachée
	tipAt   time.Time // prochain rafraîchissement de l'étiquette (durées)
	renders int
	start   time.Time
}

var (
	quit atomic.Bool
	// pendingActions : ce que les fenêtres et le menu de l'icône ont demandé, lu par
	// la boucle (leurs procédures ne font que le noter, voir wake).
	pendingActions atomic.Uint32
)

const (
	actOpenSettings  = 1 << iota // ouvrir les réglages
	actToggleAlways              // basculer « toujours afficher »
	actCloseSettings             // fermer les réglages
)

func runApp() {
	defer logPanic()
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	fs.IntVar(&opt.ui, "ui", defaultScale, "échelle des sprites : pixels d'écran par pixel de sprite (prime sur les réglages, pour ce lancement)")
	fs.DurationVar(&opt.happy, "happy", mood.DefaultConfig.HappyFor, "durée neutre/content avant de passer triste (prime sur les réglages, pour ce lancement)")
	fs.DurationVar(&opt.neutral, "neutral", mood.DefaultConfig.NeutralFor, "durée neutre, entre content et triste (prime sur les réglages, pour ce lancement)")
	fs.DurationVar(&opt.sad, "sad", mood.DefaultConfig.SadFor, "durée triste avant de s'endormir (prime sur les réglages, pour ce lancement)")
	fs.DurationVar(&opt.work, "work", 3*time.Second, "démo : durée du « travail » après un clic gauche")
	fs.IntVar(&opt.secs, "secs", 0, "quitter après N secondes")
	fs.BoolVar(&opt.tray, "tray", true, "icône de zone de notification")
	fs.BoolVar(&opt.trace, "trace", false, "affiche chaque changement d'aspect (garde la console)")
	fs.DurationVar(&opt.stats, "stats", 0, "diagnostic : écrit dans le journal l'état de la mémoire Go à cette cadence (0 : jamais)")
	fs.IntVar(&opt.dx, "dx", 0, "décale la fenêtre de N pixels vers la droite (négatif : la gauche), pour montrer plusieurs copies côte à côte")
	fs.StringVar(&opt.pin, "pin", "", "montre un seul bonhomme figé dans ce corps (normal, alert, sleeping, bench, mining, compacting), pour vérifier les sprites ; implique -demo")
	fs.BoolVar(&opt.demo, "demo", false, "trois bonhommes de démonstration (clic gauche : travaille, clic droit : attend, clic du milieu : s'en va) ; n'écoute pas les hooks")
	fs.BoolVar(&opt.background, "background", false, "lancé par le hook : n'ouvre pas la fenêtre de réglages")
	fs.Parse(os.Args[1:])
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })

	char, err := avatar.Load(assets.FS, "characters/clawd")
	if err != nil {
		fail(fmt.Errorf("chargement : %w", err))
	}
	agentFX, err := loadAgentFX()
	if err != nil {
		fail(fmt.Errorf("chargement des particules d'agents : %w", err))
	}
	// portail d'apparition : optionnel (sans lui, le clawd apparaît sans animation)
	portalFX, _ := avatar.LoadImage(assets.FS, avatar.FXDir+"/portal.png")
	terr, err := terrain.Load(assets.FS, "terrain")
	if err != nil {
		fail(fmt.Errorf("chargement du terrain : %w", err))
	}
	a := &app{
		terr:     terr,
		crowd:    crowdOf(char),
		rng:      rand.New(rand.NewSource(time.Now().UnixNano())),
		char:     char,
		agentFX:  agentFX,
		portalFX: portalFX,
		cfg:      mood.Config{HappyFor: opt.happy, NeutralFor: opt.neutral, SadFor: opt.sad, SleepFor: mood.DefaultConfig.SleepFor, BenchFor: mood.DefaultConfig.BenchFor},
		events:   make(chan session.Event, 256),
		start:    time.Now(),
	}
	a.prefs, _ = prefs.Load()
	if m := a.prefs.HappyMin; m > 0 && !explicit["happy"] {
		a.cfg.HappyFor = time.Duration(m) * time.Minute
	}
	if m := a.prefs.NeutralMin; m > 0 && !explicit["neutral"] {
		a.cfg.NeutralFor = time.Duration(m) * time.Minute
	}
	if m := a.prefs.SadMin; m > 0 && !explicit["sad"] {
		a.cfg.SadFor = time.Duration(m) * time.Minute
	}
	a.ui = clampScale(opt.ui)
	if s := a.prefs.Scale; s > 0 && !explicit["ui"] {
		a.ui = clampScale(s)
	}
	a.tipSize = clampTipSize(a.prefs.TipScale)
	guiErrors = !opt.trace
	if opt.pin != "" {
		opt.demo = true
	}
	if !opt.demo && ipc.Running() {
		// une instance tourne déjà : un second lancement à la main rouvre ses réglages
		if !opt.background {
			ipc.SendOpenSettings()
		}
		return
	}
	if legacyHook() != "" {
		showNotice(legacyHookNotice)
	}
	if !opt.trace {
		freeConsole()
	}

	setDPIAware()
	if err := createMainWindow(); err != nil {
		fail(err)
	}
	defer destroyMainWindow()
	uiApp = a
	a.ov, err = newOverlay(a)
	if err != nil {
		fail(err)
	}
	a.tip = newTooltip(a)
	a.centerX, a.top = initialPosition(a.prefs)
	a.centerX += opt.dx
	if opt.trace {
		fmt.Printf("overlay : %T, échelle d'affichage %.2f\n", a.ov, a.ov.DisplayScale())
	}

	if !opt.demo {
		// après la fenêtre principale : un événement qui arrive tout de suite réveille la boucle
		srv, err := ipc.Listen(a.post)
		if err != nil {
			if err == ipc.ErrRunning { // deux lancements simultanés : le second s'efface
				if !opt.background {
					ipc.SendOpenSettings()
				}
				return
			}
			fail(err)
		}
		defer srv.Close()
	}
	if opt.tray {
		a.tray = a.newTray()
	}
	if opt.demo {
		a.seedDemo(time.Now())
	}
	a.resize(a.slots()) // « toujours afficher » : la fenêtre est là dès le départ
	if !opt.background && !opt.demo {
		// lancé à la main (double-clic) : sans session, l'overlay est caché, et
		// l'utilisateur ne verrait rien. Les réglages portent le bouton qui branche le hook.
		a.openSettings()
	}
	// le décodage des PNG a laissé des déchets : on les rend avant de dormir
	runtime.GC()
	debug.FreeOSMemory()

	a.loop()
	a.closeSettings()
	removeDemoJournals()
	a.tip.Destroy()
	a.ov.Destroy()
	if a.tray != nil {
		a.tray.Destroy()
	}
	if opt.trace {
		fmt.Printf("rendus: %d\n", a.renders)
	}
}

// post est appelé par le serveur IPC : il confie l'événement à la boucle
// d'affichage, seule à toucher aux sessions, et la réveille.
func (a *app) post(ev session.Event) {
	if ev.Name == ipc.OpenSettings { // contrôle, pas un événement de session
		pendingActions.Or(actOpenSettings)
		wake()
		return
	}
	select {
	case a.events <- ev:
		wake()
	case <-time.After(200 * time.Millisecond):
		// la boucle ne répond plus : mieux vaut perdre l'événement que s'empiler
	}
}

// initialPosition reprend la dernière position choisie, si elle est encore sur
// un écran, sinon le haut du milieu de l'écran principal.
func initialPosition(p prefs.Prefs) (centerX, top int) {
	if p.Placed {
		if monitorFromPoint(image.Pt(p.CenterX, p.Top), monitorNull) != 0 { // toujours sur un écran
			return p.CenterX, p.Top
		}
	}
	if b, _, ok := monitorBounds(monitorFromPoint(image.Point{}, monitorPrimary)); ok {
		return b.Min.X + b.Dx()/2, b.Min.Y + topGap
	}
	return 640, topGap
}

func (a *app) savePrefs() { prefs.Save(a.prefs) }

func (a *app) setHappy(d time.Duration) {
	a.cfg.HappyFor, a.prefs.HappyMin = d, int(d/time.Minute)
	a.savePrefs()
}

func clampScale(s int) int { return max(minScale, min(maxScale, s)) }

// setScale change la taille des clawd : le socle d'herbe et la fenêtre suivent,
// autour du même axe.
func (a *app) setScale(s int) {
	s = clampScale(s)
	if s == a.ui {
		return
	}
	a.ui, a.prefs.Scale = s, s
	a.savePrefs()
	a.n = -1 // force la reconstruction de la fenêtre, de l'îlot et de la forme
	a.resize(a.slots())
}

// clampTipSize borne la taille de l'étiquette ; 0 (jamais réglée) donne le défaut.
func clampTipSize(s int) int {
	if s == 0 {
		s = defaultTipSize
	}
	return max(minTipSize, min(maxTipSize, s))
}

// setTipSize change la taille de l'étiquette de survol, sans toucher à l'overlay.
func (a *app) setTipSize(s int) {
	s = clampTipSize(s)
	if s == a.tipSize {
		return
	}
	a.tipSize, a.prefs.TipScale = s, s
	a.savePrefs()
}

func (a *app) setNeutral(d time.Duration) {
	a.cfg.NeutralFor, a.prefs.NeutralMin = d, int(d/time.Minute)
	a.savePrefs()
}

func (a *app) setSad(d time.Duration) {
	a.cfg.SadFor, a.prefs.SadMin = d, int(d/time.Minute)
	a.savePrefs()
}

// setAlways règle l'affichage de l'îlot même sans session.
func (a *app) setAlways(on bool) {
	a.prefs.AlwaysShow = on
	a.savePrefs()
	a.resize(a.slots())
	a.dirty = true
}

func (a *app) loop() {
	var deadline, nextStats, trimAt time.Time
	if opt.secs > 0 {
		deadline = a.start.Add(time.Duration(opt.secs) * time.Second)
	}
	for {
		now := time.Now()
		if quit.Load() || !deadline.IsZero() && !now.Before(deadline) {
			return
		}
		a.runActions()
		a.drainPointers()
		if a.drain(now) {
			trimAt = now.Add(trimAfter)
		}
		if !trimAt.IsZero() && !now.Before(trimAt) {
			// une rafale d'événements a gonflé le tas : on le rend au système
			trimAt = time.Time{}
			debug.FreeOSMemory()
		}
		next := a.update(now)
		var tipWake time.Time // l'étiquette visible se rafraîchit chaque seconde
		if a.tipView != nil {
			if !now.Before(a.tipAt) {
				a.refreshTip(now)
			}
			if a.tipView != nil {
				tipWake = a.tipAt
			}
		}
		for _, t := range []time.Time{deadline, nextStats, trimAt, tipWake} {
			if !t.IsZero() && (next.IsZero() || t.Before(next)) {
				next = t
			}
		}
		if opt.stats > 0 && !now.Before(nextStats) {
			nextStats = now.Add(opt.stats)
			logMemory(len(a.store.Sessions()), len(a.views))
		}
		if a.dirty && a.n > 0 {
			a.render()
		}
		a.dirty = false
		if a.set != nil {
			a.set.draw(now)
			if t := now.Add(settingsTick); next.IsZero() || t.Before(next) {
				next = t
			}
		}

		wait := time.Duration(-1) // rien n'est programmé : on dort jusqu'à un message, sans CPU
		if !next.IsZero() {
			wait = max(0, time.Until(next))
		}
		if waitMessages(wait) {
			return
		}
	}
}

// runActions applique ce que les fenêtres et le menu de l'icône ont demandé (leurs
// procédures ne font que le noter : elles tournent au milieu de la pompe de messages).
func (a *app) runActions() {
	act := pendingActions.Swap(0)
	if act&actOpenSettings != 0 {
		a.openSettings()
	}
	if act&actToggleAlways != 0 {
		a.setAlways(!a.prefs.AlwaysShow)
	}
	if act&actCloseSettings != 0 {
		a.closeSettings()
		debug.FreeOSMemory() // la fenêtre libérée, les déchets de son image aussi
	}
}

// drain applique les événements des hooks arrivés depuis le dernier passage et
// dit s'il y en avait.
func (a *app) drain(now time.Time) (any bool) {
	for {
		select {
		case ev := <-a.events:
			a.store.Apply(ev, now)
			any = true
		default:
			return any
		}
	}
}

// update retire les sessions parties, résout l'aspect de chaque bonhomme et
// renvoie le prochain réveil nécessaire (zéro : aucun).
func (a *app) update(now time.Time) (next time.Time) {
	upd := func(t time.Time) {
		if !t.IsZero() && (next.IsZero() || t.Before(next)) {
			next = t
		}
	}
	a.store.Prune(now, proc.Alive)
	a.sync(now)
	a.appeared = true // le peuplement initial est passé : les suivants arrivent par un portail
	for i, v := range a.views {
		if !v.dying.IsZero() { // il se désagrège : une image par dustFrame, jusqu'au bout (sync le retire)
			v.dustAt = now.Sub(v.dying)
			a.dirty = true
			wake := now.Add(dustFrame) // pas « next » : c'est le résultat de update
			if end := v.dying.Add(snap.Duration); end.Before(wake) {
				wake = end
			}
			upd(wake)
			continue
		}
		if !v.born.IsZero() { // il arrive : portail + chute, en position neutre
			v.bornAt = now.Sub(v.born)
			if v.bornAt < portalDur {
				a.dirty = true
				wake := now.Add(portalFrame)
				if end := v.born.Add(portalDur); end.Before(wake) {
					wake = end
				}
				upd(wake)
				continue
			}
			v.born, v.bornAt = time.Time{}, 0 // apparition finie : vue normale désormais
		}
		state := v.sess.Snapshot(now)
		look := a.cfg.Resolve(state, now)
		if opt.pin != "" { // vérification des sprites : un aspect fixe, sans fin
			look = mood.Look{Body: mood.Body(opt.pin), Emotion: mood.Neutral}
			if look.Body == mood.BodySleeping {
				look.Emotion = mood.Sleeping
			}
		}
		if look != v.look || v.animStart.IsZero() {
			if opt.trace {
				fmt.Printf("%6.1fs  %-14s %s/%s\n", now.Sub(a.start).Seconds(), v.sess.Project, look.Body, look.Emotion)
			}
			v.look, v.animStart = look, now
		}
		// chaque animation (corps, bulle…) suit sa propre période, lue dans le manifest
		elapsed := now.Sub(v.animStart)
		v.pose = a.char.At(string(look.Body), elapsed)
		if d := a.char.NextChange(string(look.Body), elapsed); d > 0 {
			upd(now.Add(d))
		}
		if opt.pin == "" {
			upd(a.cfg.NextChange(state, now))
		}
		// particules des sous-agents, au-dessus de la tête (une par agent vivant)
		var states []session.AgentState
		if opt.pin == "" {
			states = v.sess.AgentStates(now, state.Activity == mood.Idle)
		}
		if flip := a.layoutAgents(i, v, states, now); !flip.IsZero() {
			upd(flip)
		}
		if sig := particlesSig(v.particles); sig != v.partSig {
			a.dirty = true
			v.partSig = sig
		}
		if !v.drawn || v.look != v.drawLook || v.pose.Body != v.drawPose.Body || !slices.Equal(v.pose.FX, v.drawPose.FX) {
			a.dirty = true
		}
	}
	upd(a.store.NextCheck(now))
	return next
}

// slots : nombre d'emplacements voulus. Sans session, rien ; ou un îlot vide si
// l'utilisateur demande de toujours afficher (sans île, il n'y aurait rien à voir).
func (a *app) slots() int {
	if n := len(a.views); n > 0 {
		return n
	}
	if a.prefs.AlwaysShow && !a.prefs.HideIsland {
		return 1
	}
	return 0
}

// sync aligne les vues sur les sessions du Store (les maxShown plus récentes) et
// redimensionne la fenêtre quand leur nombre change. Un bonhomme dont la session
// part (ou qui cède sa place à une plus récente) se désagrège d'abord, à sa place :
// il ne la quitte, et les autres ne se resserrent, qu'une fois la désagrégation finie.
func (a *app) sync(now time.Time) {
	all := a.store.Sessions()
	all = all[max(0, len(all)-maxShown):]
	here := func(v *view) bool { return v.dying.IsZero() }
	if len(all) == len(a.views) && !slices.ContainsFunc(a.views, func(v *view) bool { return !here(v) || !slices.Contains(all, v.sess) }) {
		return
	}
	var views []*view
	for _, v := range a.views {
		switch {
		case !here(v) && now.Sub(v.dying) >= snap.Duration: // désagrégé : il quitte sa place
		case here(v) && !slices.Contains(all, v.sess):
			a.startDust(v, now)
			views = append(views, v)
		default:
			views = append(views, v)
		}
	}
	for _, s := range all { // les nouvelles sessions, à la suite (le Store les range par arrivée)
		if !slices.ContainsFunc(views, func(v *view) bool { return here(v) && v.sess == s }) {
			v := &view{sess: s}
			if a.appeared && a.portalFX != nil { // un nouveau venu tombe d'un portail
				v.born = now
			}
			views = append(views, v)
		}
	}
	if slices.Equal(views, a.views) {
		return
	}
	clear(a.views) // les vues retirées ne retiennent plus leur session
	a.views = views
	a.dirty = true
	a.resize(a.slots())
}

// resize adapte la fenêtre au nombre d'emplacements, autour du même axe vertical.
func (a *app) resize(n int) {
	if n == a.n {
		return
	}
	a.n = n
	if n == 0 {
		a.ov.Hide()
		a.shown = false
		return
	}
	u := a.ui
	f := a.frameFor(n)
	a.W, a.H, a.ground, a.below, a.slotsX0 = f.w, f.h, f.ground, f.below, f.g.slotsX0
	island := image.NewNRGBA(image.Rect(0, 0, a.W, a.H)) // île cachée : rien
	if f.g.sections > 0 {
		island = a.buildIsland(f.g.sections, f.g.islandX*u, f.ground-a.terr.Feet*u)
	}
	a.ov.Resize(a.W, a.H, island)
	// la fenêtre change de taille : elle ne doit pas déborder de l'écran ni passer sous
	// la barre des tâches (centerX et top gardent le choix de l'utilisateur)
	a.ov.SetPosition(a.fit(a.centerX-a.W/2, a.top))
	if !a.shown {
		a.ov.Show()
		a.shown = true
	}
	a.dirty = true
}

// startDust fige le bonhomme dans sa pose et prépare sa désagrégation.
func (a *app) startDust(v *view, now time.Time) {
	v.dying, v.dustAt = now, 0
	if a.tipView == v {
		a.hideTip()
	}
	ls, err := a.char.Layers(string(v.look.Body), v.pose, string(v.look.Emotion))
	if err != nil { // jamais résolu, donc jamais dessiné : il part sans rien montrer
		v.dying = now.Add(-snap.Duration)
		return
	}
	// la pose composée, dans le repère de l'emplacement : x depuis le bord gauche du
	// corps, y depuis la ligne des pieds
	var r image.Rectangle
	for _, l := range ls {
		r = r.Union(l.Image.Bounds().Add(l.At))
	}
	img := image.NewNRGBA(r)
	for _, l := range ls {
		draw.Draw(img, l.Image.Bounds().Add(l.At), l.Image, image.Point{}, draw.Over)
	}
	v.dust = snap.New(img, image.Pt(r.Min.X, r.Min.Y-ls[0].Image.Bounds().Dy()), a.rng)
}

// dustOrigin : où se trouve, dans la fenêtre (pixels d'art), le repère de la
// désagrégation du bonhomme i : le bord gauche de son corps, sur la ligne des pieds.
func (a *app) dustOrigin(i int) image.Point {
	return image.Pt(a.slotsX0+i*a.crowd.slot, a.ground/a.ui)
}

// dustLayer : la désagrégation du bonhomme i à l'instant du dernier update, dessinée
// dans une image de la taille de la fenêtre (pixels d'art), refaite à chaque image.
func (a *app) dustLayer(i int, v *view) []placed {
	w, h := a.W/a.ui, a.H/a.ui
	if v.dustBuf == nil || v.dustBuf.Rect.Dx() != w || v.dustBuf.Rect.Dy() != h {
		v.dustBuf = image.NewNRGBA(image.Rect(0, 0, w, h))
	}
	clear(v.dustBuf.Pix)
	if v.dust != nil {
		v.dust.Draw(v.dustBuf, a.dustOrigin(i), v.dustAt)
	}
	return []placed{{img: v.dustBuf, transient: true}}
}

// portalLayer : l'apparition du bonhomme i à l'instant du dernier update, dessinée
// dans une image de la taille de la fenêtre (pixels d'art), refaite à chaque image.
func (a *app) portalLayer(i int, v *view) []placed {
	w, h := a.W/a.ui, a.H/a.ui
	if v.dustBuf == nil || v.dustBuf.Rect.Dx() != w || v.dustBuf.Rect.Dy() != h {
		v.dustBuf = image.NewNRGBA(image.Rect(0, 0, w, h))
	}
	clear(v.dustBuf.Pix)
	a.drawPortal(v.dustBuf, i, v.bornAt)
	return []placed{{img: v.dustBuf, transient: true}}
}

// drawPortal dessine l'apparition du bonhomme i à l'instant t dans buf (pixels d'art,
// taille de la fenêtre) : un portail ovale qui s'ouvre depuis son centre au-dessus de
// l'emplacement, et le clawd neutre qui en tombe tout droit jusqu'au sol.
func (a *app) drawPortal(buf *image.NRGBA, i int, t time.Duration) {
	slotX := a.slotsX0 + i*a.crowd.slot
	centerX := slotX + spriteW/2
	ground := a.ground / a.ui
	ls, err := a.char.Layers("normal", a.char.At("normal", 0), "neutral") // corps neutre, statique
	if err != nil {
		return
	}
	landTop := ground - ls[0].Image.Bounds().Dy() // y du haut du corps une fois posé
	// le portail (derrière), juste au-dessus de la tête, ouvert/fermé depuis son centre
	ph, cy := 0, 0
	if a.portalFX != nil {
		ph = a.portalFX.Bounds().Dy()
		cy = landTop - 2 - ph/2
		if sc := portalScale(t); sc > 0 {
			pw := a.portalFX.Bounds().Dx()
			sw, sh := max(1, int(math.Round(float64(pw)*sc))), max(1, int(math.Round(float64(ph)*sc)))
			scaleBlit(buf, a.portalFX, centerX-sw/2, cy-sh/2, sw, sh)
		}
	}
	// le clawd neutre (devant) tombe du portail jusqu'au sol, tout droit
	if top, show := fallTop(t, cy-ph/2, landTop); show {
		for _, l := range ls {
			r := l.Image.Bounds().Add(image.Pt(slotX+l.At.X, top+l.At.Y))
			draw.Draw(buf, r, l.Image, l.Image.Bounds().Min, draw.Over)
		}
	}
}

// portalScale : l'échelle du portail à l'instant t — 0→1 à l'ouverture, 1 pendant la
// chute, puis 1→0 à la fermeture.
func portalScale(t time.Duration) float64 {
	switch {
	case t < portalOpen:
		x := float64(t) / float64(portalOpen)
		return 1 - (1-x)*(1-x) // ouverture qui ralentit
	case t < portalShut:
		return 1
	case t < portalDur:
		x := float64(t-portalShut) / float64(portalDur-portalShut)
		return 1 - x*x // fermeture qui accélère
	}
	return 0
}

// fallTop : le y (pixels d'art) du haut du corps du clawd qui tombe, en ligne droite
// de startTop à landTop ; show est faux tant qu'il n'a pas commencé à tomber.
func fallTop(t time.Duration, startTop, landTop int) (top int, show bool) {
	switch {
	case t < fallStart:
		return 0, false
	case t >= fallEnd:
		return landTop, true
	}
	x := float64(t-fallStart) / float64(fallEnd-fallStart)
	return startTop + int(math.Round(float64(landTop-startTop)*x)), true
}

// scaleBlit dessine src agrandi/réduit (au plus proche) dans dst, coin en (dx,dy),
// taille dw×dh, en composant par-dessus ce qui s'y trouve ; ce qui déborde est coupé.
func scaleBlit(dst, src *image.NRGBA, dx, dy, dw, dh int) {
	sb := src.Bounds()
	for y := 0; y < dh; y++ {
		ty := dy + y
		if ty < 0 || ty >= dst.Rect.Dy() {
			continue
		}
		sy := sb.Min.Y + y*sb.Dy()/dh
		for x := 0; x < dw; x++ {
			sc := src.NRGBAAt(sb.Min.X+x*sb.Dx()/dw, sy)
			tx := dx + x
			if sc.A == 0 || tx < 0 || tx >= dst.Rect.Dx() {
				continue
			}
			if sc.A == 255 {
				dst.SetNRGBA(tx, ty, sc)
				continue
			}
			d := dst.NRGBAAt(tx, ty)
			ia := 255 - uint32(sc.A)
			dst.SetNRGBA(tx, ty, color.NRGBA{
				uint8((uint32(sc.R)*uint32(sc.A) + uint32(d.R)*ia) / 255),
				uint8((uint32(sc.G)*uint32(sc.A) + uint32(d.G)*ia) / 255),
				uint8((uint32(sc.B)*uint32(sc.A) + uint32(d.B)*ia) / 255),
				uint8(uint32(sc.A) + uint32(d.A)*ia/255),
			})
		}
	}
}

// dismiss : clic du milieu, en démo. Le bonhomme visé s'en va (sa session se
// termine) : de quoi voir sa désagrégation.
func (a *app) dismiss(x, y float32) {
	if v, ok := a.viewAt(x, y); ok && opt.demo {
		a.post(session.Event{Session: v.sess.ID, Name: "SessionEnd"})
	}
}

// seedDemo remplit le Store de trois sessions qui montrent chacune un aspect.
func (a *app) seedDemo(now time.Time) {
	if opt.pin != "" { // un seul bonhomme, dont l'aspect est imposé par update
		a.store.Apply(session.Event{Session: "pin", Name: "SessionStart", Cwd: opt.pin, Transcript: demoJournal(55, "Épinglé : "+opt.pin)}, now)
		return
	}
	a.store.Apply(session.Event{Session: "démarrage", Name: "SessionStart", Cwd: "démarrage", Transcript: demoJournal(35, "Refonte de la page d'accueil")}, now)
	a.store.Apply(session.Event{Session: "fini", Name: "Stop", Cwd: "réponse finie", Transcript: demoJournal(70, "Correction du bug de pagination dans la liste")}, now)
	a.store.Apply(session.Event{Session: "attend", Name: "PermissionRequest", Cwd: "attend", Tool: "Bash", Transcript: demoJournal(92, "Un titre de session volontairement très long pour montrer que le nom est coupé à cinquante caractères")}, now)
	a.demoVisitor() // un visiteur qui arrive par un portail puis repart, en boucle
}

// demoVisitor fait, en boucle, arriver un clawd par un portail puis repartir en
// poussière : de quoi voir les deux animations en démo.
func (a *app) demoVisitor() {
	time.AfterFunc(2*time.Second, func() {
		a.post(session.Event{Session: "visiteur", Name: "SessionStart", Cwd: "portail", Transcript: demoJournal(20, "Débarqué d'un portail")})
		time.AfterFunc(2500*time.Millisecond, func() {
			a.post(session.Event{Session: "visiteur", Name: "SessionEnd"})
			a.demoVisitor()
		})
	})
}

// demoJournal écrit un journal factice dont le dernier message remplit percent % d'une
// fenêtre de 200 000 tokens, pour montrer la barre de contexte en démo. Le fichier est
// retiré à la sortie (voir removeDemoJournals).
func demoJournal(percent int, title string) string {
	if len(demoJournals) == 0 { // restes d'une démo tuée sans sortie propre
		stale, _ := filepath.Glob(filepath.Join(os.TempDir(), "peons-demo-*.jsonl"))
		for _, p := range stale {
			if st, err := os.Stat(p); err == nil && time.Since(st.ModTime()) > 10*time.Minute {
				os.Remove(p)
			}
		}
	}
	f, err := os.CreateTemp("", "peons-demo-*.jsonl")
	if err != nil {
		return ""
	}
	defer f.Close()
	fmt.Fprintf(f, `{"type":"ai-title","aiTitle":%q}`+"\n", title)
	fmt.Fprintf(f, `{"type":"assistant","message":{"model":"claude-opus-4-5","usage":{"input_tokens":%d,"output_tokens":0}}}`+"\n", 200_000*percent/100)
	demoJournals = append(demoJournals, f.Name())
	return f.Name()
}

var demoJournals []string

func removeDemoJournals() {
	for _, p := range demoJournals {
		os.Remove(p)
	}
}

// viewAt donne le bonhomme dont un pixel opaque est sous le point (x, y) de la
// fenêtre ; l'herbe ne compte pas (elle sert à glisser la fenêtre). Ce que dessine
// un bonhomme déborde de son écart (sa bulle passe au-dessus du suivant) : tous
// sont essayés, le dernier dessiné d'abord.
func (a *app) viewAt(x, y float32) (*view, bool) {
	for i := len(a.views) - 1; i >= 0; i-- {
		v := a.views[i]
		if !v.dying.IsZero() || !v.born.IsZero() { // il s'en va ou il arrive : on ne le pointe pas
			continue
		}
		for _, p := range a.layout(i, v) {
			dx, dy := int(x)-p.at.X, int(y)-p.at.Y
			if dx < 0 || dy < 0 { // la division tronque vers zéro : à gauche ou au-dessus, ce n'est pas le pixel 0
				continue
			}
			px, py := dx/a.ui, dy/a.ui
			if image.Pt(px, py).In(p.img.Bounds()) && p.img.NRGBAAt(px, py).A != 0 {
				return v, true
			}
		}
	}
	return nil, false
}

func (a *app) selectAt(x, y float32) {
	if v, ok := a.viewAt(x, y); ok {
		a.selected = v.sess.ID
	}
}

// click : clic simple sur la fenêtre. Un clic gauche choisit le bonhomme (les
// réglages montrent son détail) et met le terminal de sa session au premier plan. En démo, il le fait travailler puis terminer ;
// le clic droit le fait attendre une réponse.
func (a *app) click(x, y float32, right bool) {
	v, ok := a.viewAt(x, y)
	if !ok {
		return
	}
	id := v.sess.ID
	if !right {
		a.selected = id
		if pid := v.sess.PID; pid != 0 { // met son terminal au premier plan, pour la retrouver
			go proc.Focus(pid, v.sess.Title())
		}
	}
	if !opt.demo {
		return
	}
	if right {
		a.post(session.Event{Session: id, Name: "PermissionRequest", Tool: "Bash"})
		return
	}
	a.post(session.Event{Session: id, Name: "UserPromptSubmit"})
	time.AfterFunc(opt.work, func() { a.post(session.Event{Session: id, Name: "Stop"}) })
}

// layout renvoie les calques affichés maintenant pour la vue, placés à l'écran
// (pixels) ; pour un bonhomme qui s'en va, sa désagrégation.
func (a *app) layout(i int, v *view) []placed {
	if !v.dying.IsZero() {
		return a.dustLayer(i, v)
	}
	if !v.born.IsZero() {
		return a.portalLayer(i, v)
	}
	ls, err := a.char.Layers(string(v.look.Body), v.pose, string(v.look.Emotion))
	if err != nil {
		panic(err)
	}
	// le corps et ses effets, puis les particules des sous-agents au-dessus de la tête
	return append(a.place(i, ls), v.particles...)
}

// place positionne des calques dans l'emplacement i : l'image du corps (premier
// calque) pose ses pieds sur la ligne du sol.
func (a *app) place(i int, ls []avatar.Sprite) []placed {
	u := a.ui
	x0 := (a.slotsX0 + i*a.crowd.slot) * u
	y0 := a.ground - ls[0].Image.Bounds().Dy()*u
	out := make([]placed, len(ls))
	for k, l := range ls {
		out[k] = placed{img: l.Image, at: image.Pt(x0+l.At.X*u, y0+l.At.Y*u)}
	}
	return out
}

type placed struct {
	img       *image.NRGBA
	at        image.Point
	transient bool // redessinée à chaque image (désagrégation) : pas de cache de texture
}

const (
	// agentBase : bas d'une particule, en pixels d'art au-dessus du sol — juste
	// au-dessus de la tête. Hauteur fixe (les particules ne sautent pas quand le corps
	// respire ou change de taille) ; chaque agent y ajoute son propre décalage.
	agentBase = 29
	// agentStaggerMax : décalage de hauteur maximal d'une particule (tiré de son id),
	// pour qu'elles ne flottent pas toutes au même niveau.
	agentStaggerMax = 3
	agentStep       = 10 // écart horizontal voulu d'une particule à la suivante (≥ 2 px entre sprites)
	agentCell       = 7  // largeur de la plus grande particule ; les plus petites y sont centrées
)

// loadAgentFX charge les trois sprites des particules de sous-agents (idle, working, throttle).
func loadAgentFX() (map[session.AgentKind]*image.NRGBA, error) {
	files := map[session.AgentKind]string{
		session.AgentIdle:     avatar.FXDir + "/agent_idle.png",
		session.AgentWorking:  avatar.FXDir + "/agent_working.png",
		session.AgentThrottle: avatar.FXDir + "/agent_throttle.png",
	}
	out := make(map[session.AgentKind]*image.NRGBA, len(files))
	for kind, name := range files {
		img, err := avatar.LoadImage(assets.FS, name)
		if err != nil {
			return nil, err
		}
		out[kind] = img
	}
	return out, nil
}

// layoutAgents place les particules des sous-agents du bonhomme i, en une ligne
// centrée au-dessus de sa tête : une par agent vivant, chacune flottant de ±1 px à
// son propre rythme (tiré de son id, désynchronisé des autres et du corps). La ligne
// se resserre pour tenir dans la fenêtre. Renvoie le prochain instant où une
// particule change de hauteur (pour reprogrammer un rendu), ou zéro s'il n'y en a pas.
func (a *app) layoutAgents(i int, v *view, states []session.AgentState, now time.Time) (nextFlip time.Time) {
	v.particles = v.particles[:0]
	if len(states) == 0 {
		return time.Time{}
	}
	u := a.ui
	wArt := a.W / u
	center := a.slotsX0 + i*a.crowd.slot + spriteW/2 // centre horizontal du corps, en pixels d'art
	step := agentStep
	if n := len(states); n > 1 { // resserrer si la ligne ne tient pas dans la fenêtre
		if avail := wArt - 2 - agentCell; avail < (n-1)*step {
			step = max(1, avail/(n-1))
		}
	}
	rowW := (len(states)-1)*step + agentCell
	start := max(1, min(center-rowW/2, wArt-1-rowW)) // ne pas déborder de la fenêtre
	for k, s := range states {
		img := a.agentFX[s.Kind]
		if img == nil {
			continue
		}
		pw, ph := img.Bounds().Dx(), img.Bounds().Dy()
		period, phase, stagger := agentHover(s.ID)
		if f := nextHoverFlip(now, period, phase); nextFlip.IsZero() || f.Before(nextFlip) {
			nextFlip = f
		}
		x := start + k*step + (agentCell-pw)/2                        // sprite centré dans sa cellule
		top := agentBase + stagger + hoverUp(now, period, phase) + ph // haut, en px d'art au-dessus du sol
		v.particles = append(v.particles, placed{img: img, at: image.Pt(x*u, a.ground-top*u), transient: true})
	}
	return nextFlip
}

// agentHover donne le flottement d'une particule, tiré de son agent_id : période et
// phase (stables, différentes d'un agent à l'autre, aucune synchro) et un décalage de
// hauteur fixe pour qu'elles ne soient pas toutes au même niveau.
func agentHover(id string) (period time.Duration, phase float64, stagger int) {
	h := fnv.New32a()
	h.Write([]byte(id))
	v := h.Sum32()
	period = time.Duration(700+v%800) * time.Millisecond // cycle complet 0,7–1,5 s
	phase = float64((v>>9)%1000) / 1000
	stagger = int((v >> 20) % (agentStaggerMax + 1))
	return period, phase, stagger
}

// hoverUp vaut 1 pendant la moitié haute du cycle, 0 pendant l'autre : la particule
// monte d'un pixel puis redescend.
func hoverUp(now time.Time, period time.Duration, phase float64) int {
	if hoverFrac(now, period, phase) < 0.5 {
		return 1
	}
	return 0
}

// nextHoverFlip : l'instant du prochain changement de hauteur de la particule.
func nextHoverFlip(now time.Time, period time.Duration, phase float64) time.Time {
	f := hoverFrac(now, period, phase)
	boundary := 0.5
	if f >= 0.5 {
		boundary = 1
	}
	return now.Add(time.Duration((boundary - f) * float64(period)))
}

func hoverFrac(now time.Time, period time.Duration, phase float64) float64 {
	return math.Mod(float64(now.UnixNano())/float64(period)+phase, 1)
}

// particlesSig résume l'aspect courant des particules (sprite et position), pour
// savoir s'il faut redessiner.
func particlesSig(ps []placed) string {
	if len(ps) == 0 {
		return ""
	}
	var b strings.Builder
	for _, p := range ps {
		fmt.Fprintf(&b, "%p@%d,%d;", p.img, p.at.X, p.at.Y)
	}
	return b.String()
}

// manualSections : le nombre de sections centrales choisi par l'utilisateur, ou 0
// quand la taille de l'île est automatique.
func (a *app) manualSections() int {
	if a.prefs.IslandManual {
		return max(1, min(maxSections, a.prefs.IslandSections))
	}
	return 0
}

// islandSections : nombre de sections centrales de l'île pour n bonhommes. En
// automatique, juste assez pour les porter avec la zone de garde de chaque côté ;
// sinon le choix de l'utilisateur.
func (a *app) islandSections(n int) int {
	return islandGeometry(a.terr, a.crowd, n, a.manualSections(), 0).sections
}

// buildIsland assemble l'île de k sections centrales, agrandie à l'échelle de la
// fenêtre, en (x, y) d'une image de la taille de la fenêtre.
func (a *app) buildIsland(k, x, y int) *image.NRGBA {
	for len(a.picks) < k { // les sections déjà tirées gardent leur variante
		a.picks = append(a.picks, a.rng.Intn(len(a.terr.Centers)))
	}
	src := a.terr.Build(a.picks[:k])
	u := a.ui
	out := image.NewNRGBA(image.Rect(0, 0, a.W, a.H))
	b := src.Bounds()
	for sy := 0; sy < b.Dy(); sy++ {
		for sx := 0; sx < b.Dx(); sx++ {
			i := sy*src.Stride + 4*sx
			if src.Pix[i+3] == 0 {
				continue
			}
			for yy := 0; yy < u; yy++ {
				dy := y + sy*u + yy
				if dy < 0 || dy >= a.H {
					continue
				}
				for xx := 0; xx < u; xx++ {
					if dx := x + sx*u + xx; dx >= 0 && dx < a.W {
						copy(out.Pix[dy*out.Stride+4*dx:dy*out.Stride+4*dx+4], src.Pix[i:i+4])
					}
				}
			}
		}
	}
	return out
}

// setIslandAuto règle la taille automatique de l'île.
func (a *app) setIslandAuto(auto bool) {
	if auto != a.prefs.IslandManual {
		return
	}
	if !auto && a.prefs.IslandSections == 0 {
		a.prefs.IslandSections = a.islandSections(max(1, len(a.views))) // part de la taille actuelle
	}
	a.prefs.IslandManual = !auto
	a.savePrefs()
	a.n = -1 // reconstruit la fenêtre et l'île
	a.resize(a.slots())
}

// setIslandHidden cache ou remontre l'île. Les bonhommes ne bougent pas à l'écran :
// la fenêtre se refait autour d'eux, puis se recale dans l'écran s'il le faut.
func (a *app) setIslandHidden(hide bool) {
	if hide == a.prefs.HideIsland {
		return
	}
	n := max(1, a.slots())
	before := a.frameFor(n)
	a.prefs.HideIsland = hide
	after := a.frameFor(n)
	a.centerX, a.top = keepBodies(a.centerX, a.top, before, after, a.ui)
	if a.prefs.Placed {
		a.prefs.CenterX, a.prefs.Top = a.centerX, a.top
	}
	a.savePrefs()
	a.n = -1
	a.resize(a.slots())
}

// setIslandSections règle le nombre de sections centrales (taille manuelle).
func (a *app) setIslandSections(k int) {
	k = max(1, min(maxSections, k))
	if k == a.prefs.IslandSections {
		return
	}
	a.prefs.IslandSections = k
	a.savePrefs()
	a.n = -1
	a.resize(a.slots())
}
