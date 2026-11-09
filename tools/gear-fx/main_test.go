package main

import (
	"image"
	"math"
	"testing"

	"peons/assets"
	"peons/internal/avatar"
	"peons/internal/gears"
)

const tooth = 2 * math.Pi / gears.Teeth // un pas de dent, en radians

// Les deux roues ne se recouvrent à aucun angle, mesuré finement (pas seulement aux
// pixels) : si les dents se touchaient, l'animation montrerait deux roues qui se
// traversent.
func TestMeshNeverOverlaps(t *testing.T) {
	p := newPair()
	const sub = 8 // échantillons par pixel et par axe
	worst := 0
	for k := 0; k < 120; k++ {
		theta := tooth * float64(k) / 120
		thetaB := p.thetaB(theta)
		overlap := 0
		for y := 0; y < p.size.Y*sub; y++ {
			for x := 0; x < p.size.X*sub; x++ {
				fx, fy := (float64(x)+0.5)/sub, (float64(y)+0.5)/sub
				if p.a.solid(theta, fx, fy) && p.b.solid(thetaB, fx, fy) {
					overlap++
				}
			}
		}
		worst = max(worst, overlap)
	}
	if worst != 0 {
		t.Errorf("les roues se recouvrent (%d échantillons au pire) : dents trop larges ou entraxe trop court", worst)
	}
}

// L'engrènement est serré : les dents de l'une sont bien dans les creux de l'autre,
// pas deux roues qui tournent chacune dans leur coin. À tout moment, les deux roues
// sont à moins de 1,2 px d'art l'une de l'autre.
func TestMeshIsTight(t *testing.T) {
	p := newPair()
	gap := 0.0
	for k := 0; k < 40; k++ {
		theta := tooth * float64(k) / 40
		thetaB := p.thetaB(theta)
		best := math.Inf(1)
		var as, bs [][2]float64
		const sub = 4
		for y := 0; y < p.size.Y*sub; y++ {
			for x := 0; x < p.size.X*sub; x++ {
				fx, fy := (float64(x)+0.5)/sub, (float64(y)+0.5)/sub
				if p.a.solid(theta, fx, fy) {
					as = append(as, [2]float64{fx, fy})
				}
				if p.b.solid(thetaB, fx, fy) {
					bs = append(bs, [2]float64{fx, fy})
				}
			}
		}
		for _, a := range as {
			for _, b := range bs {
				if d := math.Hypot(a[0]-b[0], a[1]-b[1]); d < best {
					best = d
				}
			}
		}
		gap = math.Max(gap, best)
	}
	if gap > 1.2 {
		t.Errorf("jeu maximal entre les roues : %.2f px d'art, attendu au plus 1,2 (elles ne s'emboîtent pas)", gap)
	}
}

// La 2e roue est en diagonale : ni alignée horizontalement, ni à la verticale ; et
// l'entraxe laisse juste le jeu qu'il faut (tête d'une roue contre pied de l'autre).
func TestSecondGearIsDiagonal(t *testing.T) {
	p := newPair()
	dx, dy := p.b.cx-p.a.cx, p.a.cy-p.b.cy // dy > 0 : B est au-dessus de A
	if dx <= 0 || dy <= 0 {
		t.Fatalf("B doit être en haut à droite de A : dx=%.2f dy=%.2f", dx, dy)
	}
	if deg := math.Atan2(dy, dx) * 180 / math.Pi; deg < 15 || deg > 45 {
		t.Errorf("inclinaison %.1f°, attendue « légèrement en diagonale » (15 à 45°)", deg)
	}
	if got, min := math.Hypot(dx, dy), tipR+rootR; got < min || got > min+0.9 {
		t.Errorf("entraxe %.2f, attendu entre %.2f et %.2f (jeu étroit mais non nul)", got, min, min+0.9)
	}
	// les centres tombent sur un coin de pixel : la roue est symétrique sur la grille
	for _, g := range []gear{p.a, p.b} {
		if g.cx != math.Floor(g.cx) || g.cy != math.Floor(g.cy) {
			t.Errorf("centre (%.2f, %.2f) : doit tomber sur un coin de pixel", g.cx, g.cy)
		}
	}
}

// Les deux roues sont identiques (même forme, même taille) : seule leur position et
// leur phase diffèrent. Chacune tient dans 16×16 pixels.
func TestGearsAreIdentical(t *testing.T) {
	p := newPair()
	const side = 16
	shape := func(g gear) (m [side][side]bool, n int) {
		for y := 0; y < side; y++ {
			for x := 0; x < side; x++ {
				m[y][x] = g.solid(0, g.cx-side/2+float64(x)+0.5, g.cy-side/2+float64(y)+0.5)
				if m[y][x] {
					n++
				}
			}
		}
		return m, n
	}
	a, na := shape(p.a)
	b, _ := shape(p.b)
	if a != b {
		t.Fatal("les deux roues n'ont pas la même forme")
	}
	if na == 0 {
		t.Fatal("une roue vide")
	}
}

// Une roue dont une dent ou un creux est sur un axe est exactement symétrique : par
// rapport à l'axe vertical, à l'axe horizontal et aux diagonales. C'est ce qui fait un
// dessin « bien régulier » ; entre deux, elle tourne.
func TestKeyframesAreSymmetric(t *testing.T) {
	const side = 16
	g := gear{cx: side / 2, cy: side / 2}
	shape := func(theta float64) (m [side][side]bool) {
		for y := 0; y < side; y++ {
			for x := 0; x < side; x++ {
				m[y][x] = g.solid(theta, float64(x)+0.5, float64(y)+0.5)
			}
		}
		return m
	}
	for _, theta := range []float64{0, tooth / 2, tooth, -tooth / 2, 3 * tooth} {
		m := shape(theta)
		for y := 0; y < side; y++ {
			for x := 0; x < side; x++ {
				if m[y][x] != m[y][side-1-x] || m[y][x] != m[side-1-y][x] || m[y][x] != m[x][y] {
					t.Fatalf("theta=%.3f : la roue n'est pas symétrique en (%d,%d)", theta, x, y)
				}
			}
		}
	}
	// et elle a bien huit dents : une de 2 px au milieu de chaque côté, sur le bord de l'image
	m := shape(0)
	if !(m[0][7] && m[0][8] && m[15][7] && m[15][8] && m[7][0] && m[8][0] && m[7][15] && m[8][15]) {
		t.Error("les quatre dents des axes (2 px de large) doivent toucher le bord de l'image")
	}
	if m[0][6] || m[0][9] {
		t.Error("une dent d'axe fait 2 px de large, pas plus")
	}
}

// Elles tournent en sens contraires, du même arc.
func TestCounterRotation(t *testing.T) {
	p := newPair()
	d := 0.01
	if got := (p.thetaB(d) - p.thetaB(0)) / d; math.Abs(got+1) > 1e-9 {
		t.Errorf("rapport %.4f, attendu -1 (même taille, sens inverse)", got)
	}
}

// Les PNG de assets/fx sont ceux que l'outil dessine aujourd'hui : si on retouche la
// forme ou la palette sans relancer `go run ./tools/gear-fx`, ce test le dit.
func TestAssetsMatchTheGenerator(t *testing.T) {
	p := newPair()
	for k := 0; k < gears.Frames; k++ {
		got, err := avatar.LoadImage(assets.FS, gears.File(k))
		if err != nil {
			t.Fatal(err)
		}
		want := p.render(pose(k))
		if got.Rect.Size() != want.Rect.Size() || string(got.Pix) != string(want.Pix) {
			t.Fatalf("%s n'est pas à jour : relancer `go run ./tools/gear-fx`", gears.File(k))
		}
	}
}

// Quatre couleurs au plus pour toutes les poses, des pixels pleins ou transparents (pas
// de demi-teintes), et chaque pose est différente de la précédente (et de la suivante,
// en bouclant) : sinon la rotation ferait du surplace.
func TestPosesAreCleanPixelArt(t *testing.T) {
	p := newPair()
	colors := map[[4]uint8]bool{}
	var prev, first *image.NRGBA
	for k := 0; k < gears.Frames; k++ {
		img := p.render(pose(k))
		for i := 0; i < len(img.Pix); i += 4 {
			c := [4]uint8{img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3]}
			switch c[3] {
			case 0:
			case 255:
				colors[c] = true
			default:
				t.Fatalf("pose %d : pixel à moitié transparent %v", k, c)
			}
		}
		if prev != nil && string(prev.Pix) == string(img.Pix) {
			t.Errorf("poses %d et %d identiques : trop d'images pour la finesse du dessin", k-1, k)
		}
		if first == nil {
			first = img
		}
		prev = img
	}
	if string(prev.Pix) == string(first.Pix) {
		t.Error("la dernière pose est identique à la première")
	}
	if len(colors) > 4 {
		t.Errorf("%d couleurs, attendu 4 au plus", len(colors))
	}
	// et une pose de plus est la première : après un pas de dent, le dessin boucle
	if string(p.render(pose(gears.Frames)).Pix) != string(first.Pix) {
		t.Error("après un pas de dent, le dessin ne revient pas à la première pose")
	}
}
