package main

import (
	"fmt"
	"image"
	"math"
	"slices"
	"time"
)

// tooltip est l'étiquette qui apparaît au survol d'un bonhomme : le nom de la
// session, son état depuis quand, et une barre « inversée » du contexte (d'autant
// plus rouge qu'on approche de la limite). Réalisée sous Windows par une petite
// fenêtre superposée (tooltip_windows.go) ; ailleurs, pas encore d'étiquette.
type tooltip interface {
	// Show affiche (ou met à jour) l'étiquette près du bonhomme.
	Show(tipInfo)
	Hide()
	Destroy()
}

// tipInfo est ce que l'étiquette montre.
type tipInfo struct {
	Name  string // titre de la session (celui de l'onglet du terminal), sinon le dossier ; déjà tronqué
	Sub   string // le dossier du projet, sous le titre ; vide s'il n'y a pas de titre
	State string // « travaille », « attend une réponse »…
	Since string // depuis quand : « 3 min 05 s »
	Tool  string // l'outil en cours d'utilisation (Bash, Edit…), vide hors d'un outil

	// Contexte : ContextOK est faux tant que le journal de la session n'a rien donné.
	ContextOK   bool
	Fraction    float64 // part de la fenêtre occupée, entre 0 et 1
	Remaining   int     // tokens restants
	WindowSize  int
	Anchor      image.Rectangle // le bonhomme, en pixels d'écran
	ScaleFactor float64         // facteur d'agrandissement de l'étiquette, entier ou demi (voir tipScale)
}

const (
	maxNameLen = 50          // au-delà, le nom est coupé et suivi de « … »
	tipRefresh = time.Second // cadence de mise à jour d'une étiquette visible (durées)
)

// truncate coupe un texte à n caractères et marque la coupure par « … ».
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// hover : la souris est en (x, y) dans la fenêtre. L'étiquette montre le bonhomme
// dont un pixel opaque est dessous, et se cache ailleurs.
func (a *app) hover(x, y float32) {
	if a.drag.on {
		return
	}
	v, ok := a.viewAt(x, y)
	if !ok {
		a.hideTip()
		return
	}
	if v != a.tipView {
		a.tipView = v
		a.refreshTip(time.Now())
	}
}

func (a *app) hideTip() {
	if a.tipView == nil {
		return
	}
	a.tipView = nil
	a.tip.Hide()
}

// refreshTip redessine l'étiquette (les durées avancent) et programme le prochain
// rafraîchissement ; elle se cache si le bonhomme a disparu.
func (a *app) refreshTip(now time.Time) {
	i := slices.Index(a.views, a.tipView)
	if a.tipView == nil || i < 0 || !a.tipView.dying.IsZero() {
		a.hideTip()
		return
	}
	v := a.tipView
	snap := v.sess.Snapshot(now)
	info := tipInfo{
		Name:        truncate(nameOf(v.sess), maxNameLen),
		State:       stateLabel(snap, a.cfg.Resolve(snap, now)),
		Since:       fmtDur(now.Sub(snap.Since)),
		Tool:        truncate(v.sess.Tool, maxNameLen),
		ScaleFactor: tipScale(a.tipSize, a.ov.DisplayScale()),
	}
	// le titre de la session en grand ; le dossier, qui distingue les sessions sans titre, en dessous
	if title := v.sess.Title(); title != "" {
		info.Name, info.Sub = truncate(title, maxNameLen), truncate(nameOf(v.sess), maxNameLen)
	}
	if u, ok := v.sess.Context(); ok {
		info.ContextOK, info.Fraction, info.Remaining, info.WindowSize = true, u.Fraction(), u.Remaining(), u.Window
	}
	// le rectangle du corps (premier calque), en pixels d'écran
	body := a.layout(i, v)[0]
	ox, oy := a.ov.Position()
	b := body.img.Bounds()
	info.Anchor = image.Rect(ox+body.at.X, oy+body.at.Y, ox+body.at.X+b.Dx()*a.ui, oy+body.at.Y+b.Dy()*a.ui)
	a.tip.Show(info)
	a.tipAt = now.Add(tipRefresh)
}

// tipScale : le facteur de l'étiquette, c'est-à-dire sa taille réglée (size,
// indépendante de celle des clawd) multipliée par l'échelle de l'écran, arrondi au
// pas de la taille : à l'entier pour une taille entière (la police reste nette, comme
// avant les demi-pas), au demi pour une demi-taille (police lissée).
func tipScale(size, display float64) float64 {
	step := 1.0
	if size != math.Trunc(size) {
		step = tipStep
	}
	return max(1, math.Round(display*size/step)*step)
}

// fmtTokens : « 74k », « 1,2M ».
func fmtTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1000:
		return fmt.Sprintf("%dk", (n+500)/1000)
	}
	return fmt.Sprint(n)
}
