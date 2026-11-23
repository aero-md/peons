package main

import (
	"image"
	"testing"

	"peons/assets"
	"peons/internal/avatar"
	"peons/internal/mood"
	"peons/internal/prefs"
	"peons/internal/terrain"
)

func realCrowd(t *testing.T) crowd {
	t.Helper()
	char, err := avatar.Load(assets.FS, "characters/clawd")
	if err != nil {
		t.Fatal(err)
	}
	return crowdOf(char)
}

func realTerrain(t *testing.T) *terrain.Terrain {
	t.Helper()
	tr, err := terrain.Load(assets.FS, "terrain")
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// Les bonhommes ne sont jamais trop près du bord de l'île : au moins la zone de garde,
// de chaque côté, pour toute taille automatique.
func TestAutoIslandKeepsAGuardZone(t *testing.T) {
	tr, cr := realTerrain(t), realCrowd(t)
	if tr.Guard < 8 {
		t.Fatalf("zone de garde de %d px : trop étroite pour sortir de la pente du bord", tr.Guard)
	}
	for n := 1; n <= maxShown; n++ {
		g := islandGeometry(tr, cr, n, 0, islandPad)
		firstLeft := g.slotsX0 - g.islandX                            // le corps du premier, depuis le bord gauche de l'île
		lastRight := g.islandX + g.islandW - (g.slotsX0 + cr.span(n)) // le corps du dernier, jusqu'au bord droit
		if firstLeft < tr.Guard || lastRight < tr.Guard {
			t.Errorf("%d bonhommes : %d px à gauche, %d à droite, la zone de garde est de %d", n, firstLeft, lastRight, tr.Guard)
		}
		if d := firstLeft - lastRight; d < -1 || d > 1 {
			t.Errorf("%d bonhommes : marges %d et %d, les corps doivent être centrés sur l'île", n, firstLeft, lastRight)
		}
		// l'île est la plus petite qui convienne
		if g.sections > 1 && tr.Width(g.sections-1) >= cr.span(n)+2*tr.Guard {
			t.Errorf("%d bonhommes : %d sections, une de moins suffisait", n, g.sections)
		}
		// la fenêtre contient l'île et tout ce que dessinent les bonhommes, avec sa marge
		if !contains(g, cr, n) {
			t.Errorf("%d bonhommes : la fenêtre (%d) ne contient pas tout : %+v", n, g.w, g)
		}
	}
}

// contains : l'île et tout ce que dessinent les n bonhommes tiennent dans la
// fenêtre, à islandPad de ses bords.
func contains(g geometry, cr crowd, n int) bool {
	inner := image.Rect(islandPad, 0, g.w-islandPad, 1)
	first, last := g.slotsX0+cr.reach.Min.X, g.slotsX0+(n-1)*cr.slot+cr.reach.Max.X
	return g.w == g.contentW+2*islandPad && first >= inner.Min.X && last <= inner.Max.X &&
		g.islandX >= inner.Min.X && g.islandX+g.islandW <= inner.Max.X
}

// Une île manuelle plus courte que les bonhommes les laisse déborder, sans les couper.
func TestManualIslandCanBeTooShort(t *testing.T) {
	tr, cr := realTerrain(t), realCrowd(t)
	g := islandGeometry(tr, cr, 6, 1, islandPad)
	if g.sections != 1 || g.islandW != tr.Width(1) {
		t.Fatalf("manuel : %+v", g)
	}
	if !contains(g, cr, 6) {
		t.Errorf("les bonhommes ou l'île sortent de la fenêtre : %+v", g)
	}
	if g.contentW <= g.islandW {
		t.Errorf("les bonhommes devraient déborder de l'île : %+v", g)
	}
}

// testApp : de quoi disposer la fenêtre, sans écran.
func testApp(t *testing.T, p prefs.Prefs) *app {
	t.Helper()
	char, err := avatar.Load(assets.FS, "characters/clawd")
	if err != nil {
		t.Fatal(err)
	}
	return &app{char: char, terr: realTerrain(t), crowd: crowdOf(char), ui: 3, prefs: p}
}

// drawn : le rectangle (pixels d'écran) des pixels opaques que les n bonhommes
// peuvent dessiner dans la fenêtre disposée par f, toutes poses confondues.
func drawn(a *app, f frame, n int) image.Rectangle {
	a.ground, a.slotsX0 = f.ground, f.g.slotsX0
	var r image.Rectangle
	for name, b := range a.char.Bodies {
		poses := []avatar.Pose{{}}
		for _, fx := range b.FX {
			var next []avatar.Pose
			for _, p := range poses {
				for i := range a.char.FX[fx].Frames {
					next = append(next, avatar.Pose{FX: append(append([]int(nil), p.FX...), i)})
				}
			}
			poses = next
		}
		for _, p := range poses { // et chacune les yeux fermés (clignement)
			p.Blink = true
			poses = append(poses, p)
		}
		for bi := range b.Frames {
			for _, p := range poses {
				p.Body = bi
				for emotion := range a.char.Eyes {
					ls, _ := a.char.Layers(name, p, emotion)
					for i := 0; i < n; i++ {
						for _, l := range a.place(i, ls) {
							if o := opaque(l.img); !o.Empty() {
								r = r.Union(image.Rect(l.at.X+o.Min.X*a.ui, l.at.Y+o.Min.Y*a.ui, l.at.X+o.Max.X*a.ui, l.at.Y+o.Max.Y*a.ui))
							}
						}
					}
				}
			}
		}
	}
	return r
}

func opaque(img *image.NRGBA) image.Rectangle {
	var r image.Rectangle
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			if img.NRGBAAt(x, y).A != 0 {
				r = r.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return r
}

// Sans île, la fenêtre colle aux bonhommes : leurs pixels touchent ses bords gauche,
// droit et haut, et elle s'arrête à la ligne des pieds. Recalée dans l'écran, elle
// les met au ras des bords et pose leurs pieds sur la barre des tâches.
func TestBareFrameHugsTheClawds(t *testing.T) {
	a := testApp(t, prefs.Prefs{HideIsland: true})
	for n := 1; n <= maxShown; n++ {
		f := a.frameFor(n)
		if f.g.sections != 0 {
			t.Fatalf("%d bonhommes : %d sections d'île alors qu'elle est cachée", n, f.g.sections)
		}
		d := drawn(a, f, n)
		if d.Min.X != 0 || d.Max.X != f.w || d.Min.Y != 0 {
			t.Errorf("%d bonhommes : fenêtre %dx%d, dessin %v : il doit toucher les bords gauche, droit et haut", n, f.w, f.h, d)
		}
		if f.h != f.ground {
			t.Errorf("%d bonhommes : la fenêtre descend %d px sous les pieds", n, f.h-f.ground)
		}
		// lâchée sous la barre des tâches (zone utilisable jusqu'à 1032), elle remonte
		// juste assez pour que les pieds la touchent
		area := image.Rect(0, 0, 1920, 1032)
		if at := clampTo(image.Rect(100, 1000, 100+f.w, 1000+f.h), area); at.Y+f.ground != area.Max.Y {
			t.Errorf("%d bonhommes : pieds en %d, la barre des tâches commence en %d", n, at.Y+f.ground, area.Max.Y)
		}
	}
	// avec l'île, la fenêtre descend sous les pieds (le bas de l'île) et garde sa marge
	isle := testApp(t, prefs.Prefs{})
	if f := isle.frameFor(1); f.h <= f.ground || drawn(isle, f, 1).Min.X == 0 {
		t.Errorf("avec l'île : %+v", f)
	}
}

// Cacher l'île puis la remontrer ne fait pas bouger les bonhommes à l'écran : le
// premier corps et la ligne des pieds restent en place, et l'aller-retour rend
// l'ancre de départ.
func TestToggleIslandKeepsClawdsInPlace(t *testing.T) {
	with, without := testApp(t, prefs.Prefs{}), testApp(t, prefs.Prefs{HideIsland: true})
	at := func(centerX, top int, f frame) image.Point { // le pied gauche du premier corps, à l'écran
		return image.Pt(centerX-f.w/2+f.g.slotsX0*with.ui, top+f.ground)
	}
	for n := 1; n <= maxShown; n++ {
		fi, fb := with.frameFor(n), without.frameFor(n)
		cx, top := keepBodies(960, 30, fi, fb, with.ui)
		if at(cx, top, fb) != at(960, 30, fi) {
			t.Errorf("%d bonhommes : le premier corps passe de %v à %v", n, at(960, 30, fi), at(cx, top, fb))
		}
		if bx, bt := keepBodies(cx, top, fb, fi, with.ui); bx != 960 || bt != 30 {
			t.Errorf("%d bonhommes : aller-retour (%d,%d), attendu (960,30)", n, bx, bt)
		}
	}
}

// Les clawd sont serrés au point que la bulle de sommeil de l'un passe au-dessus du
// suivant : la survoler désigne son clawd, pas le voisin dont elle couvre l'écart.
func TestViewAtFindsOverhangingBubble(t *testing.T) {
	a := testApp(t, prefs.Prefs{HideIsland: true})
	f := a.frameFor(2)
	a.ground, a.slotsX0 = f.ground, f.g.slotsX0
	asleep := mood.Look{Body: mood.BodySleeping, Emotion: mood.Sleeping}
	pose := avatar.Pose{FX: []int{len(a.char.FX["sleeping_bubble"].Frames) - 1}} // la plus grande bulle
	a.views = []*view{{look: asleep, pose: pose}, {look: asleep, pose: pose}}
	ls := a.layout(0, a.views[0])
	bubble := ls[len(ls)-1]
	second := (a.slotsX0 + a.crowd.slot) * a.ui // le bord gauche du corps du second, à l'écran
	tested := 0
	b := bubble.img.Bounds()
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			sx, sy := bubble.at.X+x*a.ui+1, bubble.at.Y+y*a.ui+1
			if bubble.img.NRGBAAt(x, y).A == 0 || sx < second {
				continue
			}
			tested++
			if v, ok := a.viewAt(float32(sx), float32(sy)); !ok || v != a.views[0] {
				t.Fatalf("bulle du premier en (%d,%d) : trouvé %v (%v), attendu le premier", sx, sy, v, ok)
			}
		}
	}
	if tested == 0 {
		t.Fatal("aucun pixel de la bulle ne passe au-dessus du voisin : le test ne vérifie rien")
	}
	// le premier pixel à gauche du corps du premier n'est à personne
	if v, ok := a.viewAt(float32(a.slotsX0*a.ui-1), float32(a.ground-1)); ok {
		t.Errorf("à gauche du premier : %v", v)
	}
}
