package main

import (
	"image"
	"testing"
	"time"

	"peons/internal/burst"
	"peons/internal/gears"
	"peons/internal/prefs"
	"peons/internal/session"
)

// /resume : la nouvelle session reprend le bonhomme de l'ancienne. Il ne se désagrège
// pas et ne retombe pas d'un portail ; deux engrenages tournent au-dessus de sa tête,
// puis disparaissent. L'ordre du SessionEnd et du SessionStart n'y change rien.
func TestResumeSpinsGearsOnTheSameClawd(t *testing.T) {
	for _, endFirst := range []bool{true, false} {
		for _, hide := range []bool{false, true} {
			a := portalApp(t, prefs.Prefs{HideIsland: hide})
			base := time.Now()
			a.store.Apply(session.Event{Session: "a", Name: "UserPromptSubmit", PID: 42, Started: 7}, base)
			a.update(base)
			a.selected = "a"
			v, w, h, n := a.views[0], a.W, a.H, a.n

			end := func() {
				a.store.Apply(session.Event{Session: "a", Name: "SessionEnd", Reason: "resume"}, base.Add(10*time.Millisecond))
			}
			start := func() {
				a.store.Apply(session.Event{Session: "b", Name: "SessionStart", PID: 42, Started: 7, Source: "resume"}, base.Add(20*time.Millisecond))
			}
			if endFirst {
				end()
				a.update(base.Add(15 * time.Millisecond))
				if len(a.views) != 1 || !a.views[0].dying.IsZero() {
					t.Fatalf("endFirst=%v : l'ancien ne doit pas se désagréger en attendant sa suite", endFirst)
				}
				start()
			} else {
				start()
				end()
			}
			now := base.Add(100 * time.Millisecond)
			next := a.update(now)
			if len(a.views) != 1 || a.views[0] != v || a.n != n || a.W != w || a.H != h {
				t.Fatalf("endFirst=%v hide=%v : le même bonhomme, la même fenêtre attendus (%d bonhommes)", endFirst, hide, len(a.views))
			}
			if v.sess.ID != "b" || !v.dying.IsZero() || !v.born.IsZero() || a.selected != "b" {
				t.Fatalf("endFirst=%v : « b » reprend la place sans désagrégation ni portail (session %s, choisie %s)", endFirst, v.sess.ID, a.selected)
			}
			if v.gearFrame == nil {
				t.Fatalf("endFirst=%v : les engrenages doivent tourner", endFirst)
			}
			if next.IsZero() || next.Sub(now) > gears.Step {
				t.Errorf("pendant l'animation, prochain réveil dans %v, attendu au plus %v", next.Sub(now), gears.Step)
			}

			// ils sont posés au-dessus de la tête, centrés sur le bonhomme, tout dans la fenêtre
			ls := a.layout(0, v)
			g := ls[len(ls)-1]
			if g.img != v.gearFrame {
				t.Fatalf("la dernière couche doit être les engrenages")
			}
			size := g.img.Rect.Size()
			box := image.Rectangle{Min: g.at, Max: g.at.Add(size.Mul(a.ui))}
			if !box.In(image.Rect(0, 0, a.W, a.H)) {
				t.Errorf("engrenages %v hors de la fenêtre %dx%d", box, a.W, a.H)
			}
			if box.Max.Y > a.ground-gearBase*a.ui {
				t.Errorf("engrenages %v : doivent être au-dessus de la tête (sol %d, tête à %d px)", box, a.ground, gearBase*a.ui)
			}
			centre := (box.Min.X + box.Max.X) / 2 / a.ui
			if want := a.slotsX0 + spriteW/2; centre < want-1 || centre > want+1 {
				t.Errorf("engrenages centrés en %d, attendu près de %d", centre, want)
			}

			// ils tournent : l'image change d'un pas à l'autre
			first := v.gearFrame
			a.update(now.Add(3 * gears.Step))
			if v.gearFrame == nil || v.gearFrame == first {
				t.Error("les engrenages doivent changer d'image")
			}

			// fin : plus rien, la fenêtre n'a pas bougé
			a.update(now.Add(gears.Duration + 50*time.Millisecond))
			if v.gearFrame != nil || !v.gears.IsZero() {
				t.Error("après Duration, les engrenages disparaissent")
			}
			if ls := a.layout(0, v); len(ls) > 0 && ls[len(ls)-1].img == first {
				t.Error("la couche des engrenages doit disparaître")
			}
			if a.W != w || a.H != h {
				t.Errorf("la fenêtre a changé pendant l'animation : %dx%d, avant %dx%d", a.W, a.H, w, h)
			}
		}
	}
}

// Les engrenages tiennent dans la hauteur que la fenêtre réserve déjà : l'animation
// ne la fait pas grandir (le bonhomme ne bougerait plus à l'écran pour ceux qui l'ont placé).
func TestGearsFitTheExistingHeadroom(t *testing.T) {
	a := portalApp(t, prefs.Prefs{})
	top := gearBase + a.gearAnim().Size().Y
	if h := a.char.Height(); top > h {
		t.Errorf("engrenages jusqu'à %d px au-dessus du sol, la fenêtre n'en réserve que %d", top, h)
	}
	if reach := -a.crowd.reach.Min.Y; top > reach {
		t.Errorf("engrenages jusqu'à %d px au-dessus du sol, sans île la fenêtre n'en réserve que %d", top, reach)
	}
}

// /clear : même bonhomme, même place ; il éclate dans tous les sens puis se reconstitue
// (ni désagrégation, ni portail, ni engrenages). Il reste dans la fenêtre et au-dessus
// du sol, ne se laisse pas pointer pendant l'explosion, et reprend son dessin normal à la fin.
func TestClearBurstsTheSameClawd(t *testing.T) {
	for _, endFirst := range []bool{true, false} {
		for _, hide := range []bool{false, true} {
			a := portalApp(t, prefs.Prefs{HideIsland: hide})
			base := time.Now()
			a.store.Apply(session.Event{Session: "a", Name: "Stop", PID: 42, Started: 7}, base)
			a.update(base)
			a.selected = "a"
			v, w, h, n := a.views[0], a.W, a.H, a.n
			before := artOf(a, 0)

			end := func() {
				a.store.Apply(session.Event{Session: "a", Name: "SessionEnd", Reason: "clear"}, base.Add(10*time.Millisecond))
			}
			start := func() {
				a.store.Apply(session.Event{Session: "b", Name: "SessionStart", PID: 42, Started: 7, Source: "clear"}, base.Add(20*time.Millisecond))
			}
			if endFirst {
				end()
				a.update(base.Add(15 * time.Millisecond))
				if len(a.views) != 1 || !a.views[0].dying.IsZero() {
					t.Fatalf("endFirst=%v : l'ancien ne doit pas se désagréger en attendant sa suite", endFirst)
				}
				start()
			} else {
				start()
				end()
			}
			now := base.Add(100 * time.Millisecond)
			next := a.update(now)
			if len(a.views) != 1 || a.views[0] != v || a.n != n || a.W != w || a.H != h {
				t.Fatalf("endFirst=%v hide=%v : le même bonhomme, la même fenêtre attendus (%d bonhommes)", endFirst, hide, len(a.views))
			}
			if v.sess.ID != "b" || !v.dying.IsZero() || !v.born.IsZero() || a.selected != "b" {
				t.Fatalf("endFirst=%v : « b » reprend la place sans désagrégation ni portail (session %s, choisie %s)", endFirst, v.sess.ID, a.selected)
			}
			if v.burst == nil || !v.gears.IsZero() {
				t.Fatalf("endFirst=%v : une explosion, pas des engrenages", endFirst)
			}
			src := artOf(a, 0) // le dessin à l'instant 0 de l'explosion : le clawd tel qu'il était
			if next.IsZero() || next.Sub(now) > dustFrame {
				t.Errorf("pendant l'explosion, prochain réveil dans %v, attendu au plus %v", next.Sub(now), dustFrame)
			}
			if ls := a.layout(0, v); len(ls) != 1 || !ls[0].transient || !ls[0].img.Rect.In(image.Rect(0, 0, a.W/a.ui, a.H/a.ui)) {
				t.Fatalf("elle se dessine via une seule image transiente, dans la fenêtre")
			}
			for y := 0; y < before.Rect.Dy(); y++ {
				for x := 0; x < before.Rect.Dx(); x++ {
					if before.NRGBAAt(x, y).A == 0 {
						continue
					}
					if got, ok := a.viewAt(float32(x*a.ui+1), float32(y*a.ui+1)); ok && got == v {
						t.Fatalf("en (%d,%d), le bonhomme qui éclate est encore pointable", x, y)
					}
				}
			}

			// en plein vol : plus étalé que le clawd, entier dans la fenêtre ; rien ne passe sous le sol\n			// (hors ce que le dessin d'origine y met déjà : la roche du mineur s'enfonce dans l'herbe)
			a.update(base.Add(100*time.Millisecond + 500*time.Millisecond))
			mid := artOf(a, 0)
			if equalPix(mid, src) {
				t.Error("au bout d'une demi-seconde, rien n'a bougé")
			}
			groundRow := a.ground / a.ui
			for y := groundRow; y < mid.Rect.Dy(); y++ {
				for x := 0; x < mid.Rect.Dx(); x++ {
					if mid.NRGBAAt(x, y).A != 0 {
						t.Fatalf("pixel en (%d,%d), sous la ligne des pieds (%d)", x, y, groundRow)
					}
				}
			}

			// fin : le bonhomme reprend son dessin normal, la fenêtre n'a pas bougé
			a.update(now.Add(burst.Duration + 50*time.Millisecond))
			if v.burst != nil {
				t.Error("après Duration, l'explosion est finie")
			}
			if ls := a.layout(0, v); len(ls) > 0 && ls[0].transient && ls[0].img.Rect.Dx() == a.W/a.ui {
				t.Error("la couche d'explosion doit disparaître")
			}
			if a.W != w || a.H != h {
				t.Errorf("la fenêtre a changé pendant l'animation : %dx%d, avant %dx%d", a.W, a.H, w, h)
			}
		}
	}
}
