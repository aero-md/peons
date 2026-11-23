package main

import (
	"fmt"
	"image"
	"slices"
	"sync"

	"peons/internal/avatar"
)

// overlay est la fenêtre transparente qui porte les bonhommes et l'îlot. Sa
// réalisation est layeredOverlay (overlay_windows.go) : une fenêtre superposée
// native dont l'alpha de chaque pixel décide aussi du clic, sans région ni
// composition DWM, donc sans clignotement. L'interface permet aux tests d'en
// mettre une fausse.
type overlay interface {
	Show()
	Hide()
	// Position et SetPosition : coin haut-gauche de la fenêtre, en pixels d'écran.
	Position() (x, y int)
	SetPosition(x, y int)
	// Resize prépare une fenêtre de w×h pixels portant cet îlot (image w×h).
	Resize(w, h int, island *image.NRGBA)
	// Draw dessine l'état courant de l'application (îlot et bonhommes).
	Draw(a *app)
	// DisplayScale : facteur d'échelle de l'écran qui porte la fenêtre (1 = 100 %).
	DisplayScale() float64
	Destroy()
}

type pointerKind int

const (
	pointerDown pointerKind = iota
	pointerMove             // la souris bouge, bouton gauche enfoncé (glisser)
	pointerUp
	pointerHover // la souris bouge, sans bouton : survol
	pointerLeave // la souris quitte la fenêtre
)

// pointer est une entrée souris sur l'overlay, indépendante de la réalisation.
type pointer struct {
	kind   pointerKind
	right  bool    // bouton droit ; sinon gauche
	middle bool    // bouton du milieu (en démo : le bonhomme s'en va)
	clicks int     // 1 : simple clic, 2 : double-clic
	x, y   float32 // dans la fenêtre
	gx, gy float32 // sur l'écran
}

// Les entrées de l'overlay arrivent depuis sa procédure de fenêtre ; la boucle
// principale les traite à son passage.
var (
	pointerMu    sync.Mutex
	pointerQueue []pointer
)

// pushPointer note une entrée et réveille la boucle.
func pushPointer(p pointer) {
	pointerMu.Lock()
	pointerQueue = append(pointerQueue, p)
	pointerMu.Unlock()
	wake()
}

// drainPointers applique les entrées en attente.
func (a *app) drainPointers() {
	pointerMu.Lock()
	queued := pointerQueue
	pointerQueue = nil
	pointerMu.Unlock()
	for _, p := range queued {
		a.pointer(p)
	}
}

// pointer traite une entrée souris : glisser pour déplacer, clic pour choisir un
// bonhomme, double-clic pour ouvrir les réglages.
func (a *app) pointer(p pointer) {
	switch p.kind {
	case pointerHover:
		a.hover(p.x, p.y)
	case pointerLeave:
		a.hideTip()
	case pointerDown:
		a.hideTip() // pas d'étiquette pendant un clic ou un glisser
		if opt.trace {
			fmt.Printf("clic bouton %v, %d fois, en (%.0f,%.0f)\n", map[bool]string{false: "gauche", true: "droit"}[p.right], p.clicks, p.x, p.y)
		}
		if p.middle {
			a.dismiss(p.x, p.y)
			return
		}
		if p.right {
			a.click(p.x, p.y, true)
			return
		}
		if p.clicks >= 2 { // double-clic : les réglages, sur le bonhomme visé s'il y en a un
			a.drag.on = false
			a.selectAt(p.x, p.y)
			a.openSettings()
			return
		}
		x, y := a.ov.Position()
		a.drag.on, a.drag.moved = true, false
		a.drag.win, a.drag.mouse = image.Pt(x, y), [2]float32{p.gx, p.gy}
	case pointerMove:
		if !a.drag.on {
			return
		}
		dx, dy := p.gx-a.drag.mouse[0], p.gy-a.drag.mouse[1]
		if a.drag.moved || dx*dx+dy*dy > 16 {
			a.drag.moved = true
			a.ov.SetPosition(a.drag.win.X+int(dx), a.drag.win.Y+int(dy))
		}
	case pointerUp:
		if p.right || p.middle || !a.drag.on {
			return
		}
		a.drag.on = false
		if a.drag.moved {
			// lâché sous la barre des tâches ou hors écran : on ramène la fenêtre en vue
			x, y := a.ov.Position()
			if fx, fy := a.fit(x, y); fx != x || fy != y {
				a.ov.SetPosition(fx, fy)
				x, y = fx, fy
			}
			a.centerX, a.top = x+a.W/2, y
			a.prefs.CenterX, a.prefs.Top, a.prefs.Placed = a.centerX, a.top, true
			a.savePrefs()
		} else {
			a.click(p.x, p.y, false)
		}
	}
}

// render dessine l'overlay et note, pour chaque bonhomme, ce qui vient d'être dessiné.
func (a *app) render() {
	a.renders++
	a.ov.Draw(a)
	for _, v := range a.views {
		v.drawn, v.drawLook, v.drawPose = true, v.look, avatarPose(v.pose)
	}
}

// avatarPose copie une pose : les frames d'effet ne doivent pas changer sous nos yeux.
func avatarPose(p avatar.Pose) avatar.Pose {
	return avatar.Pose{Body: p.Body, FX: slices.Clone(p.FX), Blink: p.Blink}
}
