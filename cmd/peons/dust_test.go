package main

import (
	"image"
	"image/draw"
	"math/rand"
	"testing"
	"time"

	"peons/internal/mood"
	"peons/internal/prefs"
	"peons/internal/session"
	"peons/internal/snap"
)

// fakeOverlay : une fenêtre qui ne fait que retenir sa taille.
type fakeOverlay struct{ w, h, x, y int }

func (o *fakeOverlay) Show()                           {}
func (o *fakeOverlay) Hide()                           {}
func (o *fakeOverlay) Position() (int, int)            { return o.x, o.y }
func (o *fakeOverlay) SetPosition(x, y int)            { o.x, o.y = x, y }
func (o *fakeOverlay) Resize(w, h int, _ *image.NRGBA) { o.w, o.h = w, h }
func (o *fakeOverlay) Draw(*app)                       {}
func (o *fakeOverlay) DisplayScale() float64           { return 1 }
func (o *fakeOverlay) Destroy()                        {}

// artOf : ce que dessine le bonhomme i, composé dans une image de la fenêtre en pixels d'art.
func artOf(a *app, i int) *image.NRGBA {
	out := image.NewNRGBA(image.Rect(0, 0, a.W/a.ui, a.H/a.ui))
	for _, p := range a.layout(i, a.views[i]) {
		draw.Draw(out, p.img.Bounds().Add(p.at.Div(a.ui)), p.img, image.Point{}, draw.Over)
	}
	return out
}

// Un bonhomme dont la session part se désagrège à sa place, figé dans sa dernière
// pose : la fenêtre ne bouge pas, il n'est plus pointable, il est redessiné à
// chaque dustFrame ; en moins de 3 s il a disparu et la fenêtre se resserre sur
// les autres, dans leur ordre.
func TestLeavingClawdTurnsToDust(t *testing.T) {
	a := testApp(t, prefs.Prefs{HideIsland: true})
	a.ov, a.rng, a.cfg = &fakeOverlay{}, rand.New(rand.NewSource(1)), mood.DefaultConfig
	t0 := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	for _, id := range []string{"a", "b", "c"} {
		a.store.Apply(session.Event{Session: id, Name: "SessionStart", Cwd: id}, t0)
	}
	a.update(t0)
	if len(a.views) != 3 || a.n != 3 {
		t.Fatalf("%d bonhommes, fenêtre pour %d", len(a.views), a.n)
	}
	before, w := artOf(a, 1), a.W

	leave := t0.Add(time.Second)
	a.store.Apply(session.Event{Session: "b", Name: "SessionEnd"}, leave)
	a.update(leave)
	v := a.views[1]
	if len(a.views) != 3 || a.n != 3 || a.W != w || v.dying.IsZero() || v.sess.ID != "b" {
		t.Fatalf("au départ de b : %d bonhommes, fenêtre pour %d (%d px, avant %d), b mourant : %v", len(a.views), a.n, a.W, w, !v.dying.IsZero())
	}
	if got := artOf(a, 1); !equalPix(got, before) {
		t.Error("au départ, la désagrégation doit montrer le bonhomme tel qu'il était")
	}
	for y := 0; y < before.Rect.Dy(); y++ {
		for x := 0; x < before.Rect.Dx(); x++ {
			if before.NRGBAAt(x, y).A == 0 {
				continue
			}
			if got, ok := a.viewAt(float32(x*a.ui+1), float32(y*a.ui+1)); ok && got == v {
				t.Fatalf("en (%d,%d), le bonhomme qui s'en va est encore pointable", x, y)
			}
		}
	}
	mid := leave.Add(time.Second)
	if next := a.update(mid); next.IsZero() || next.Sub(mid) > dustFrame {
		t.Errorf("pendant la désagrégation, prochain réveil dans %v, attendu au plus %v", next.Sub(mid), dustFrame)
	}
	if equalPix(artOf(a, 1), before) {
		t.Error("au bout d'une seconde, la désagrégation n'a rien changé")
	}
	if snap.Duration >= 3*time.Second {
		t.Fatalf("désagrégation de %v : elle doit durer moins de 3 s", snap.Duration)
	}
	a.update(leave.Add(snap.Duration))
	if len(a.views) != 2 || a.n != 2 || a.views[0].sess.ID != "a" || a.views[1].sess.ID != "c" {
		t.Errorf("après la désagrégation : %d bonhommes, fenêtre pour %d", len(a.views), a.n)
	}
}

// /resume et /clear ouvrent une nouvelle session dans le même processus Claude Code :
// l'ancienne se désagrège, la nouvelle tombe d'un portail à la même place, et l'île
// ne bouge pas (ni rétrécie, ni cachée, ni agrandie entre-temps). Le SessionEnd de
// l'ancienne peut arriver avant ou après le SessionStart de la nouvelle.
func TestResumeKeepsTheSlot(t *testing.T) {
	for _, endFirst := range []bool{true, false} {
		a := portalApp(t, prefs.Prefs{})
		base := time.Now()
		a.store.Apply(session.Event{Session: "a", Name: "UserPromptSubmit", PID: 42, Started: 7}, base) // le processus de « a » (après la première vérification des processus)
		a.update(base)
		w, n := a.W, a.n

		end := func() { a.store.Apply(session.Event{Session: "a", Name: "SessionEnd"}, base) }
		start := func(at time.Duration) {
			a.store.Apply(session.Event{Session: "b", Name: "SessionStart", PID: 42, Started: 7}, base.Add(at))
		}
		steady := func(at time.Duration, want string) {
			t.Helper()
			a.update(base.Add(at))
			if len(a.views) != 1 || a.n != n || a.W != w || a.views[0].sess.ID != want {
				t.Fatalf("endFirst=%v à %v : %d bonhommes (%s), fenêtre pour %d (%d px, avant %d), attendu « %s » seul, fenêtre inchangée", endFirst, at, len(a.views), a.views[0].sess.ID, a.n, a.W, w, want)
			}
		}
		if endFirst {
			end()
			steady(0, "a")
			start(50 * time.Millisecond)
		} else {
			start(0)
			steady(0, "a")
			end()
		}
		steady(100*time.Millisecond, "a")
		if v := a.views[0]; v.dying.IsZero() || v.heir == nil || v.heir.ID != "b" {
			t.Fatalf("endFirst=%v : « a » doit se désagréger en attendant son héritier « b » : %+v", endFirst, v)
		}
		steady(snap.Duration-time.Millisecond, "a")
		steady(snap.Duration, "b")
		if v := a.views[0]; !v.dying.IsZero() || v.born.IsZero() {
			t.Errorf("endFirst=%v : « b » doit tomber d'un portail à la place de « a »", endFirst)
		}
	}
}

// Si l'héritier n'est plus là quand l'ancien a fini de se désagréger, la place est
// libérée comme pour n'importe quel départ.
func TestResumeHeirGone(t *testing.T) {
	a := portalApp(t, prefs.Prefs{})
	base := time.Now()
	a.store.Apply(session.Event{Session: "a", Name: "UserPromptSubmit", PID: 42, Started: 7}, base)
	a.update(base)
	a.store.Apply(session.Event{Session: "a", Name: "SessionEnd"}, base)
	a.store.Apply(session.Event{Session: "b", Name: "SessionStart", PID: 42, Started: 7}, base)
	a.update(base)
	a.store.Apply(session.Event{Session: "b", Name: "SessionEnd"}, base.Add(time.Second))
	a.update(base.Add(snap.Duration))
	if len(a.views) != 0 || a.n != 0 {
		t.Errorf("%d bonhommes, fenêtre pour %d : plus personne, attendu rien", len(a.views), a.n)
	}
}

// Les sous-agents d'une session apparaissent en particules au-dessus de la tête du
// clawd : une par agent, dans la fenêtre, au-dessus de la ligne des pieds. Le sprite
// suit l'état (working quand le parent travaille et l'agent outille ; throttle quand
// le parent a fini son tour et les attend).
func TestAgentParticlesAboveHead(t *testing.T) {
	a := testApp(t, prefs.Prefs{HideIsland: true})
	var err error
	if a.agentFX, err = loadAgentFX(); err != nil {
		t.Fatal(err)
	}
	a.ov, a.rng, a.cfg = &fakeOverlay{}, rand.New(rand.NewSource(1)), mood.DefaultConfig
	t0 := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	a.store.Apply(session.Event{Session: "a", Name: "UserPromptSubmit", Cwd: "proj"}, t0)
	a.store.Apply(session.Event{Session: "a", Name: "PreToolUse", Agent: "ag1", AgentType: "general-purpose"}, t0)
	a.store.Apply(session.Event{Session: "a", Name: "PreToolUse", Agent: "ag2", AgentType: "general-purpose"}, t0)
	a.update(t0)

	v := a.views[0]
	if len(v.particles) != 2 {
		t.Fatalf("%d particules, attendu 2", len(v.particles))
	}
	win := image.Rect(0, 0, a.W, a.H)
	for _, p := range v.particles {
		b := image.Rect(p.at.X, p.at.Y, p.at.X+p.img.Bounds().Dx()*a.ui, p.at.Y+p.img.Bounds().Dy()*a.ui)
		if !b.In(win) {
			t.Errorf("particule %v hors de la fenêtre %v", b, win)
		}
		if b.Max.Y >= a.ground { // son bas reste au-dessus de la ligne des pieds (donc au-dessus de la tête)
			t.Errorf("particule trop basse : bas %d, sol à %d", b.Max.Y, a.ground)
		}
		if 2*p.at.Y >= a.ground { // elle flotte dans le haut de la fenêtre
			t.Errorf("particule pas dans le haut : haut %d", p.at.Y)
		}
		if p.img != a.agentFX[session.AgentWorking] {
			t.Error("parent au travail + outil en cours : sprite working attendu")
		}
	}
	// le parent finit son tour : ses agents vivants passent « attendus » (throttle)
	a.store.Apply(session.Event{Session: "a", Name: "Stop"}, t0.Add(time.Second))
	a.update(t0.Add(time.Second))
	if len(v.particles) != 2 {
		t.Fatalf("après Stop : %d particules, attendu 2", len(v.particles))
	}
	for _, p := range v.particles {
		if p.img != a.agentFX[session.AgentThrottle] {
			t.Error("parent inactif : sprite throttle attendu")
		}
	}
}

func equalPix(a, b *image.NRGBA) bool {
	if a.Rect != b.Rect {
		return false
	}
	for i := range a.Pix {
		if a.Pix[i] != b.Pix[i] {
			return false
		}
	}
	return true
}
