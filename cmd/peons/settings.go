package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"slices"
	"strings"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"

	"peons/internal/claudecfg"
	"peons/internal/mood"
	"peons/internal/session"
)

// La fenêtre de réglages : une vraie fenêtre Windows (settings_windows.go), ouverte
// à la demande (menu de l'icône ou double-clic sur un bonhomme) et détruite à la
// fermeture, donc elle ne coûte rien tant qu'elle est fermée. Tout est dessiné par
// Go (polices embarquées de golang.org/x/image) dans une section DIB que Windows
// recopie à l'écran : ni DLL ni GPU.

const (
	setW      = 460 // largeur du contenu, en pixels logiques (multipliés par l'échelle d'affichage)
	setM      = 20  // marge
	wheelStep = 60  // pixels logiques défilés par cran de molette
)

var (
	colBg      = color.NRGBA{0x0f, 0x17, 0x2a, 0xff}
	colPanel   = color.NRGBA{0x1e, 0x29, 0x3b, 0xff}
	colBorder  = color.NRGBA{0x33, 0x41, 0x55, 0xff}
	colText    = color.NRGBA{0xe2, 0xe8, 0xf0, 0xff}
	colMuted   = color.NRGBA{0x94, 0xa3, 0xb8, 0xff}
	colBlue    = color.NRGBA{0x25, 0x63, 0xeb, 0xff}
	colBlueHi  = color.NRGBA{0x1d, 0x4e, 0xd8, 0xff}
	colGray    = color.NRGBA{0x1f, 0x29, 0x37, 0xff}
	colGrayHi  = color.NRGBA{0x37, 0x41, 0x51, 0xff}
	colGreen   = color.NRGBA{0x86, 0xef, 0xac, 0xff}
	colAmber   = color.NRGBA{0xfb, 0xbf, 0x24, 0xff}
	colRed     = color.NRGBA{0xf8, 0x71, 0x71, 0xff}
	colWorking = color.NRGBA{0x60, 0xa5, 0xfa, 0xff}
	colSad     = color.NRGBA{0xa7, 0x8b, 0xfa, 0xff}
	colCompact = color.NRGBA{0xf4, 0x72, 0xb6, 0xff}
	colAsleep  = color.NRGBA{0x64, 0x74, 0x8b, 0xff}
)

type textStyle uint8

const (
	styleBody textStyle = iota
	styleSmall
	styleBold
	styleTitle
	nStyles
)

// styleSize et styleBoldFace : taille en pixels logiques et police de chaque style.
var styleSize = [nStyles]float64{14, 12, 14, 22}
var styleBoldFace = [nStyles]bool{false, false, true, true}

type opKind uint8

const (
	opFill opKind = iota
	opFrame
	opText
)

// op est une instruction de dessin ; la liste d'instructions d'une image sert
// aussi à savoir si quelque chose a changé depuis la précédente (comparable).
type op struct {
	kind  opKind
	r     image.Rectangle // pour opText : seul Min compte (coin haut-gauche du texte)
	col   color.NRGBA
	text  string
	style textStyle
}

type hit struct {
	r   image.Rectangle
	act func()
}

type settings struct {
	a     *app
	hwnd  uintptr
	icon  uintptr
	scale float64
	faces [nStyles]font.Face
	W, H  int // taille de la zone cliente, en pixels

	// canvas est la mémoire de la section DIB (BGRA) lue comme une image RGBA :
	// rouge et bleu y sont échangés, d'où bgr à chaque couleur écrite.
	dib    dibSection
	canvas *image.RGBA

	contentH   int // hauteur du contenu au dernier build (peut dépasser H → défilement)
	scroll     int // décalage de défilement vertical, en pixels (0 : haut)
	prevScroll int // scroll du dernier rendu, pour savoir s'il faut redessiner

	ops      []op
	prev     []op
	hits     []hit
	mouse    image.Point
	hasMouse bool
	tracking bool // Windows a été prié de signaler la sortie de la souris

	legacyHook string // ancien hook de peons trouvé dans settings.json, ou ""
}

func (s *settings) p(v int) int { return int(math.Round(float64(v) * s.scale)) }

const legacyHookNotice = "Des hooks de peons sont encore dans le settings.json de Claude Code : le plugin les remplace, chaque événement serait reçu deux fois. Retire-les de ~/.claude/settings.json (entrées dont la commande finit par « peons.exe hook »)."

// legacyHook donne la commande d'un ancien hook de peons dans settings.json, ou "".
func legacyHook() string {
	dir, err := claudecfg.Dir()
	if err != nil {
		return ""
	}
	return claudecfg.LegacyCommand(dir)
}

// refreshHook relit les réglages de Claude Code à la recherche d'anciens hooks.
func (s *settings) refreshHook() { s.legacyHook = legacyHook() }

// pointerMoved note la position de la souris dans la fenêtre (survol des boutons).
func (s *settings) pointerMoved(x, y int) {
	s.mouse, s.hasMouse = image.Pt(x, y), true
}

// wheel fait défiler le contenu : notches > 0, molette vers le haut, remonte.
func (s *settings) wheel(notches float64) {
	s.scroll -= int(notches * float64(s.p(wheelStep))) // clampé au rendu
}

// release traite le relâchement du bouton gauche en (x, y) de la fenêtre.
func (s *settings) release(x, y int) {
	pt := image.Pt(x, y+s.scroll) // coordonnées du contenu (défilement inclus)
	for i := len(s.hits) - 1; i >= 0; i-- {
		if pt.In(s.hits[i].r) {
			s.hits[i].act()
			break
		}
	}
}

// ensureCanvas (re)crée l'image de la fenêtre quand sa taille change.
func (s *settings) ensureCanvas() {
	if s.canvas != nil && s.dib.w == s.W && s.dib.h == s.H {
		return
	}
	s.dib.create(s.W, s.H)
	s.canvas = &image.RGBA{Pix: s.dib.bits, Stride: 4 * s.W, Rect: image.Rect(0, 0, s.W, s.H)}
	s.prev = nil // tout est à redessiner
}

// draw construit l'image puis, si elle a changé (contenu ou défilement), la dessine
// décalée du scroll courant. Le contenu peut dépasser la fenêtre : il défile.
func (s *settings) draw(now time.Time) {
	s.contentH = s.build(now)
	maxScroll := max(0, s.contentH-s.H)
	s.scroll = max(0, min(s.scroll, maxScroll))
	if s.W <= 0 || s.H <= 0 { // réduite dans la barre des tâches
		return
	}
	s.ensureCanvas()
	if slices.Equal(s.ops, s.prev) && s.scroll == s.prevScroll {
		return
	}
	s.prev = append(s.prev[:0], s.ops...)
	s.prevScroll = s.scroll
	s.paintRect(s.canvas.Rect, colBg)
	for _, o := range s.ops {
		r := o.r.Sub(image.Pt(0, s.scroll))
		if r.Min.Y > s.H || r.Max.Y < -s.p(40) { // entièrement hors de la vue
			continue
		}
		switch o.kind {
		case opFill:
			s.paintRect(r, o.col)
		case opFrame:
			s.paintFrame(r, o.col)
		case opText:
			s.paintText(r.Min.X, r.Min.Y, o.text, o.style, o.col)
		}
	}
	s.scrollbar(maxScroll)
	s.present()
}

// scrollbar dessine l'indicateur de défilement sur le bord droit quand le contenu
// dépasse la fenêtre.
func (s *settings) scrollbar(maxScroll int) {
	if maxScroll <= 0 || s.contentH <= 0 {
		return
	}
	bw, x0 := s.p(6), s.W-s.p(9)
	thumbH := max(s.p(28), s.H*s.H/s.contentH)
	ty := (s.H - thumbH) * s.scroll / maxScroll
	s.paintRect(image.Rect(x0, 0, x0+bw, s.H), colPanel)
	s.paintRect(image.Rect(x0, ty, x0+bw, ty+thumbH), colMuted)
}

// bgr : la couleur telle qu'elle s'écrit dans le canevas (rouge et bleu échangés).
func bgr(c color.NRGBA) color.RGBA { return color.RGBA{R: c.B, G: c.G, B: c.R, A: 0xff} }

func (s *settings) paintRect(r image.Rectangle, c color.NRGBA) {
	draw.Draw(s.canvas, r, &image.Uniform{bgr(c)}, image.Point{}, draw.Src)
}

// paintFrame trace le contour d'un pixel, à l'intérieur de r.
func (s *settings) paintFrame(r image.Rectangle, c color.NRGBA) {
	s.paintRect(image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+1), c)
	s.paintRect(image.Rect(r.Min.X, r.Max.Y-1, r.Max.X, r.Max.Y), c)
	s.paintRect(image.Rect(r.Min.X, r.Min.Y, r.Min.X+1, r.Max.Y), c)
	s.paintRect(image.Rect(r.Max.X-1, r.Min.Y, r.Max.X, r.Max.Y), c)
}

// paintText dessine un texte dont le coin haut-gauche est en (x, y).
func (s *settings) paintText(x, y int, text string, st textStyle, c color.NRGBA) {
	face := s.faces[st]
	d := &font.Drawer{Dst: s.canvas, Src: &image.Uniform{bgr(c)}, Face: face, Dot: fixed.P(x+1, y+face.Metrics().Ascent.Ceil()+1)}
	d.DrawString(text)
}

func (s *settings) measure(text string, st textStyle) int {
	return font.MeasureString(s.faces[st], text).Ceil()
}

// lineH : hauteur d'une ligne de texte du style.
func (s *settings) lineH(st textStyle) int {
	m := s.faces[st].Metrics()
	return (m.Ascent + m.Descent).Ceil() + 2
}

func (s *settings) fillRect(r image.Rectangle, c color.NRGBA) {
	s.ops = append(s.ops, op{kind: opFill, r: r, col: c})
}

func (s *settings) frameRect(r image.Rectangle, c color.NRGBA) {
	s.ops = append(s.ops, op{kind: opFrame, r: r, col: c})
}

// text pose un texte dont le coin haut-gauche est en (x, y) et rend sa largeur.
func (s *settings) text(x, y int, str string, st textStyle, c color.NRGBA) int {
	if str == "" {
		return 0
	}
	s.ops = append(s.ops, op{kind: opText, r: image.Rect(x, y, x, y), col: c, text: str, style: st})
	return s.measure(str, st)
}

// textRight : comme text, aligné à droite sur xr.
func (s *settings) textRight(xr, y int, str string, st textStyle, c color.NRGBA) {
	s.text(xr-s.measure(str, st), y, str, st, c)
}

// hovered : r est en coordonnées du contenu, la souris en coordonnées fenêtre ; on
// ajoute le défilement pour les comparer.
func (s *settings) hovered(r image.Rectangle) bool {
	return s.hasMouse && s.mouse.Add(image.Pt(0, s.scroll)).In(r)
}

func (s *settings) onClick(r image.Rectangle, act func()) { s.hits = append(s.hits, hit{r, act}) }

// button : texte centré dans r.
func (s *settings) button(r image.Rectangle, text string, primary bool, act func()) {
	bg, hi := colGray, colGrayHi
	if primary {
		bg, hi = colBlue, colBlueHi
	}
	if s.hovered(r) {
		bg = hi
	}
	s.fillRect(r, bg)
	s.frameRect(r, colBorder)
	tw, th := s.measure(text, styleBody), s.lineH(styleBody)
	s.text(r.Min.X+(r.Dx()-tw)/2, r.Min.Y+(r.Dy()-th)/2, text, styleBody, colText)
	s.onClick(r, act)
}

// section : titre en petites capitales grises et filet.
func (s *settings) section(y int, title string) int {
	s.text(s.p(setM), y, strings.ToUpper(title), styleSmall, colMuted)
	ly := y + s.lineH(styleSmall) + s.p(3)
	s.fillRect(image.Rect(s.p(setM), ly, s.p(setW-setM), ly+1), colBorder)
	return ly + s.p(10)
}

// checkbox : case à cocher et son libellé ; un clic n'importe où sur la ligne la bascule.
// Rend la hauteur de la ligne.
func (s *settings) checkbox(y int, label string, on bool, toggle func()) int {
	x0, xr := s.p(setM), s.p(setW-setM)
	cb := s.p(18)
	cr := image.Rect(x0, y, xr, y+s.p(26))
	box := image.Rect(x0, y+(cr.Dy()-cb)/2, x0+cb, y+(cr.Dy()-cb)/2+cb)
	boxCol := colBorder
	if s.hovered(cr) {
		boxCol = colMuted
	}
	s.fillRect(box, colPanel)
	s.frameRect(box, boxCol)
	if on {
		s.fillRect(box.Inset(s.p(4)), colBlue)
	}
	s.text(x0+cb+s.p(10), y+(cr.Dy()-s.lineH(styleBody))/2, label, styleBody, colText)
	s.onClick(cr, toggle)
	return cr.Dy() + s.p(8)
}

// stepper : libellé à gauche, [−] valeur [+] à droite. Rend la hauteur de la ligne.
// Sans minus ni plus (nil), le réglage est grisé et ne répond pas.
func (s *settings) stepper(y int, name, value string, minus, plus func()) int {
	box := s.p(26)
	xr := s.p(setW - setM)
	th := s.lineH(styleBody)
	enabled := minus != nil && plus != nil
	textCol := colText
	if !enabled {
		textCol = colMuted
	}
	s.text(s.p(setM), y+(box-th)/2, name, styleBody, textCol)
	pr := image.Rect(xr-box, y, xr, y+box)
	vw := s.p(78)
	mr := image.Rect(pr.Min.X-vw-box, y, pr.Min.X-vw, y+box)
	if enabled {
		s.button(pr, "+", false, plus)
		s.button(mr, "−", false, minus)
	} else {
		for _, r := range []image.Rectangle{pr, mr} {
			s.fillRect(r, colBg)
			s.frameRect(r, colBorder)
		}
		s.text(pr.Min.X+(box-s.measure("+", styleBody))/2, y+(box-th)/2, "+", styleBody, colBorder)
		s.text(mr.Min.X+(box-s.measure("−", styleBody))/2, y+(box-th)/2, "−", styleBody, colBorder)
	}
	s.text(mr.Max.X+(vw-s.measure(value, styleBody))/2, y+(box-th)/2, value, styleBody, textCol)
	return box + s.p(8)
}

// build compose la fenêtre et renvoie sa hauteur. Les zones cliquables sont
// reconstruites en même temps que l'image : elles sont toujours à jour.
func (s *settings) build(now time.Time) (height int) {
	a := s.a
	s.ops, s.hits = s.ops[:0], s.hits[:0]
	x0, xr := s.p(setM), s.p(setW-setM)

	y := s.p(18)
	s.text(x0, y, "Peons", styleTitle, colText)
	y += s.lineH(styleTitle) + s.p(14)

	// --- Claude Code
	y = s.section(y, "Claude Code")
	hint, col := "Les hooks viennent du plugin : claude plugin install peons --marketplace aero-md/peons", colMuted
	if s.legacyHook != "" {
		hint, col = legacyHookNotice, colAmber
	}
	for _, line := range wrap(hint, 66) {
		s.text(x0, y-s.p(2), line, styleSmall, col)
		y += s.lineH(styleSmall)
	}
	y += s.p(4)

	// --- Réglages
	y = s.section(y+s.p(6), "Réglages")
	y += s.checkbox(y, "Toujours afficher, même sans session", a.prefs.AlwaysShow, func() { a.setAlways(!a.prefs.AlwaysShow) })

	y += s.stepper(y, "Taille des clawd", fmt.Sprintf("%d×", a.ui),
		func() { a.setScale(a.ui - 1) }, func() { a.setScale(a.ui + 1) })
	s.text(x0, y-s.p(4), "1× : un pixel d'écran par pixel du sprite, 2× : deux, etc.", styleSmall, colMuted)
	y += s.lineH(styleSmall) + s.p(8)

	y += s.stepper(y, "Taille de l'étiquette", fmt.Sprintf("%d×", a.tipSize),
		func() { a.setTipSize(a.tipSize - 1) }, func() { a.setTipSize(a.tipSize + 1) })
	s.text(x0, y-s.p(4), "L'étiquette qui apparaît au survol d'un clawd, à part des clawd.", styleSmall, colMuted)
	y += s.lineH(styleSmall) + s.p(8)

	y += s.checkbox(y, "Afficher l'île", !a.prefs.HideIsland, func() { a.setIslandHidden(!a.prefs.HideIsland) })
	if a.prefs.HideIsland {
		for _, line := range wrap("La fenêtre se réduit aux clawd : ils se calent au ras de l'écran et de la barre des tâches. Sans session, rien n'est affiché.", 66) {
			s.text(x0, y-s.p(4), line, styleSmall, colMuted)
			y += s.lineH(styleSmall)
		}
		y += s.p(8)
	} else {
		y += s.checkbox(y, "Taille de l'île automatique", !a.prefs.IslandManual, func() { a.setIslandAuto(a.prefs.IslandManual) })
		sections := a.islandSections(max(1, len(a.views)))
		var less, more func()
		if a.prefs.IslandManual {
			less, more = func() { a.setIslandSections(sections - 1) }, func() { a.setIslandSections(sections + 1) }
		}
		y += s.stepper(y, "Sections de l'île", fmt.Sprintf("%d", sections), less, more)
		islandHint := "Elle grandit avec le nombre de clawd."
		if a.prefs.IslandManual {
			islandHint = "Une section de plus allonge l'île de 16 pixels du sprite."
		}
		s.text(x0, y-s.p(4), islandHint, styleSmall, colMuted)
		y += s.lineH(styleSmall) + s.p(8)
	}
	y += s.stepper(y, "Content pendant", fmtSpan(a.cfg.HappyFor),
		func() { a.setHappy(stepSpan(a.cfg.HappyFor, -1)) }, func() { a.setHappy(stepSpan(a.cfg.HappyFor, +1)) })
	y += s.stepper(y, "Neutre pendant", fmtSpan(a.cfg.NeutralFor),
		func() { a.setNeutral(stepSpan(a.cfg.NeutralFor, -1)) }, func() { a.setNeutral(stepSpan(a.cfg.NeutralFor, +1)) })
	y += s.stepper(y, "Triste pendant", fmtSpan(a.cfg.SadFor),
		func() { a.setSad(stepSpan(a.cfg.SadFor, -1)) }, func() { a.setSad(stepSpan(a.cfg.SadFor, +1)) })
	s.text(x0, y-s.p(2), "Puis il s'endort jusqu'à la prochaine sollicitation.", styleSmall, colMuted)
	y += s.lineH(styleSmall) + s.p(10)

	// --- Sessions
	all := a.store.Sessions()
	y = s.section(y+s.p(6), fmt.Sprintf("Sessions (%d)", len(all)))
	if len(all) == 0 {
		for _, line := range []string{
			"Aucune session Claude Code pour l'instant.",
			"Elle apparaît à son prochain événement (prompt, outil, réponse).",
		} {
			s.text(x0, y, line, styleBody, colMuted)
			y += s.lineH(styleBody) + s.p(2)
		}
	}
	chosen := a.effective()
	const maxRows = 8
	rowH := s.p(28)
	for i := len(all) - 1; i >= 0 && len(all)-1-i < maxRows; i-- { // la plus récente d'abord
		sess := all[i]
		snap := sess.Snapshot(now)
		look := a.cfg.Resolve(snap, now)
		rr := image.Rect(x0-s.p(6), y, xr+s.p(6), y+rowH)
		switch {
		case sess == chosen:
			s.fillRect(rr, colPanel)
		case s.hovered(rr):
			s.fillRect(rr, colGray)
		}
		dot := s.p(10)
		s.fillRect(image.Rect(x0, y+(rowH-dot)/2, x0+dot, y+(rowH-dot)/2+dot), stateColor(snap, look))
		ty := y + (rowH-s.lineH(styleBody))/2
		name := ellipsize(nameOf(sess), 26)
		s.text(x0+dot+s.p(10), ty, name, styleBold, colText)
		s.textRight(xr, ty, stateLabel(snap, look)+" · "+fmtDur(now.Sub(snap.Since)), styleBody, colMuted)
		id := sess.ID
		s.onClick(rr, func() { a.selected = id })
		y += rowH
	}
	if len(all) > maxRows {
		s.text(x0, y+s.p(2), fmt.Sprintf("… et %d autres", len(all)-maxRows), styleSmall, colMuted)
		y += s.lineH(styleSmall) + s.p(4)
	}

	// --- Détail de la session choisie
	if chosen != nil {
		snap := chosen.Snapshot(now)
		look := a.cfg.Resolve(snap, now)
		y = s.section(y+s.p(10), "Détail : "+ellipsize(nameOf(chosen), 30))
		colX := x0 + s.p(140)
		row := func(name, value string) {
			s.text(x0, y, name, styleBody, colMuted)
			s.text(colX, y, value, styleBody, colText)
			y += s.lineH(styleBody) + s.p(2)
		}
		row("État", stateLabel(snap, look))
		row("Humeur", emotionLabel(look.Emotion))
		row("Depuis", fmtDur(now.Sub(snap.Since)))
		row("Ouverte depuis", fmtDur(now.Sub(chosen.Opened)))
		if u, ok := chosen.Context(); ok {
			row("Contexte", fmt.Sprintf("%d %% (%s restants)", int(u.Fraction()*100+0.5), fmtTokens(u.Remaining())))
		}
		if chosen.Tool != "" {
			row("Outil en cours", chosen.Tool)
		}
		if len(chosen.Recent) > 0 {
			y += s.p(4)
			s.text(x0, y, "Récent", styleBody, colMuted)
			y += s.lineH(styleBody) + s.p(2)
			for i := len(chosen.Recent) - 1; i >= 0 && len(chosen.Recent)-1-i < 4; i-- {
				s.text(x0+s.p(12), y, "· "+ellipsize(chosen.Recent[i], 56), styleSmall, colText)
				y += s.lineH(styleSmall) + s.p(1)
			}
		}
	}

	// --- Pied
	y += s.p(18)
	qw := s.p(140)
	s.button(image.Rect(xr-qw, y, xr, y+s.p(30)), "Quitter peons", false, func() { quit.Store(true) })
	return y + s.p(30) + s.p(18)
}

// effective : la session choisie, sinon la plus récemment active.
func (a *app) effective() *session.Session {
	var best *session.Session
	for _, s := range a.store.Sessions() {
		if s.ID == a.selected {
			return s
		}
		if best == nil || s.Last.After(best.Last) {
			best = s
		}
	}
	return best
}

func nameOf(s *session.Session) string {
	if s.Project != "" {
		return s.Project
	}
	return s.ID
}

func stateLabel(snap mood.Snapshot, look mood.Look) string {
	switch snap.Activity {
	case mood.Working:
		return "travaille"
	case mood.Awaiting:
		return "attend une réponse"
	case mood.Compacting:
		return "compacte son contexte"
	}
	switch look.Emotion {
	case mood.Happy:
		return "content"
	case mood.Sad:
		return "triste"
	case mood.Sleeping:
		return "endormi"
	}
	return "prêt"
}

func emotionLabel(e mood.Emotion) string {
	switch e {
	case mood.Happy:
		return "content"
	case mood.Sad:
		return "triste"
	case mood.Sleeping:
		return "endormi"
	}
	return "neutre"
}

func stateColor(snap mood.Snapshot, look mood.Look) color.NRGBA {
	switch snap.Activity {
	case mood.Working:
		return colWorking
	case mood.Awaiting:
		return colAmber
	case mood.Compacting:
		return colCompact
	}
	switch look.Emotion {
	case mood.Happy:
		return colGreen
	case mood.Sad:
		return colSad
	case mood.Sleeping:
		return colAsleep
	}
	return colMuted
}

// fmtDur : « 12 s », « 3 min 05 s », « 1 h 02 min ».
func fmtDur(d time.Duration) string {
	d = max(d, 0).Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min %02d s", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%d h %02d min", int(d.Hours()), int(d.Minutes())%60)
}

// fmtSpan : durée de réglage, en minutes quand elle est ronde.
func fmtSpan(d time.Duration) string {
	if d%time.Minute == 0 {
		return fmt.Sprintf("%d min", int(d/time.Minute))
	}
	return fmtDur(d)
}

// stepSpan avance une durée d'une minute, de cinq au-delà de dix, entre 1 et 120 min.
func stepSpan(d time.Duration, dir int) time.Duration {
	m := int(math.Round(d.Minutes()))
	step := 1
	if m > 10 || m == 10 && dir > 0 {
		step = 5
	}
	return time.Duration(max(1, min(120, m+dir*step))) * time.Minute
}

func ellipsize(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// wrap coupe un texte en lignes d'au plus n caractères, aux espaces.
func wrap(s string, n int) []string {
	var lines []string
	line := ""
	for _, w := range strings.Fields(s) {
		if line != "" && len([]rune(line))+1+len([]rune(w)) > n {
			lines = append(lines, line)
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += w
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}
