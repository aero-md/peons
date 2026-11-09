package burst

import (
	"image"
	"image/color"
	"math"
	"math/rand"
	"testing"
	"time"
)

// clawdLike : un bloc opaque de 30×20 à deux couleurs, posé en (5,7).
func clawdLike() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 40, 30))
	for y := 7; y < 27; y++ {
		for x := 5; x < 35; x++ {
			c := color.NRGBA{215, 119, 87, 255}
			if (x+y)%5 == 0 {
				c = color.NRGBA{20, 20, 20, 255}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

var wide = image.Rect(-60, -60, 100, 100)

func draw(b *Burst, t time.Duration) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(-60, -60, 120, 120))
	b.Draw(dst, image.Point{}, t)
	return dst
}

func equal(a, b *image.NRGBA) bool {
	for i := range a.Pix {
		if a.Pix[i] != b.Pix[i] {
			return false
		}
	}
	return true
}

// Au départ et à la fin, c'est l'image d'origine, pixel pour pixel : rien n'est perdu.
func TestStartsAndEndsWhole(t *testing.T) {
	src := clawdLike()
	b := New(src, image.Pt(10, -4), wide, rand.New(rand.NewSource(1)))
	want := image.NewNRGBA(image.Rect(-60, -60, 120, 120))
	for y := 0; y < 30; y++ {
		for x := 0; x < 40; x++ {
			if c := src.NRGBAAt(x, y); c.A != 0 {
				want.SetNRGBA(x+10, y-4, c)
			}
		}
	}
	if !equal(draw(b, 0), want) {
		t.Error("à t=0, l'image doit être celle d'origine")
	}
	if !equal(draw(b, Duration), want) {
		t.Error("à la fin, l'image doit être reconstituée à l'identique")
	}
	if Duration >= 3*time.Second {
		t.Errorf("durée %v : l'animation doit durer moins de 3 s", Duration)
	}
}

// Ça part dans tous les sens : à l'arrêt, les pixels sont bien plus loin du centre
// qu'au départ, et dans toutes les directions (pas de vent, pas de côté privilégié).
func TestExplodesInAllDirections(t *testing.T) {
	b := New(clawdLike(), image.Point{}, wide, rand.New(rand.NewSource(2)))
	var left, right, up, down int
	far := 0
	for _, p := range b.parts {
		q := p.peak()
		switch {
		case q.X < p.home.X-2:
			left++
		case q.X > p.home.X+2:
			right++
		}
		switch {
		case q.Y < p.home.Y-2:
			up++
		case q.Y > p.home.Y+2:
			down++
		}
		if math.Hypot(float64(q.X-p.home.X), float64(q.Y-p.home.Y)) >= minDist-1 {
			far++
		}
	}
	n := len(b.parts)
	for name, c := range map[string]int{"gauche": left, "droite": right, "haut": up, "bas": down} {
		if c < n/6 {
			t.Errorf("%s : seulement %d pixels sur %d partent de ce côté", name, c, n)
		}
	}
	if far < n*9/10 {
		t.Errorf("%d pixels sur %d seulement partent à plus de %.0f px", far, n, minDist-1)
	}
	if b.Bounds().Dx() <= 30+10 || b.Bounds().Dy() <= 20+10 {
		t.Errorf("étendue %v : l'explosion doit déborder nettement de l'image", b.Bounds())
	}
}

// Ils s'arrêtent : entre la fin de la projection et le premier retour, rien ne bouge ;
// puis tout revient, et le retour est étalé (les pixels ne reviennent pas d'un bloc).
func TestStopsThenReturnsStaggered(t *testing.T) {
	b := New(clawdLike(), image.Point{}, wide, rand.New(rand.NewSource(3)))
	stopped := draw(b, time.Duration(outFor*float64(time.Second)))
	if still := draw(b, time.Duration((holdTill-0.01)*float64(time.Second))); !equal(stopped, still) {
		t.Error("entre l'arrêt et le retour, rien ne doit bouger")
	}
	// au milieu des départs de retour, une partie des pixels est revenue, une autre pas encore
	midSec := holdTill + backFor*0.8 + spread/2
	mid := time.Duration(midSec * float64(time.Second))
	home, away := 0, 0
	for _, p := range b.parts {
		if p.pos(mid.Seconds()) == p.home {
			home++
		} else {
			away++
		}
	}
	if home == 0 || away == 0 {
		t.Errorf("en plein retour : %d revenus, %d encore loin : le retour doit être étalé", home, away)
	}
	// aucun pixel ne s'éloigne pendant le retour
	for _, p := range b.parts {
		prev := math.Inf(1)
		for s := holdTill + 0.25; s <= Duration.Seconds(); s += 0.02 {
			if p.delay > s {
				continue
			}
			q := p.pos(s)
			d := math.Hypot(float64(q.X-p.home.X), float64(q.Y-p.home.Y))
			if d > prev+1 { // +1 : arrondi
				t.Fatalf("un pixel s'éloigne pendant le retour (%.1f → %.1f)", prev, d)
			}
			prev = d
		}
	}
}

// Rien ne sort de limit (la fenêtre, le sol) ni de Bounds, à aucun instant.
func TestStaysWithinLimit(t *testing.T) {
	limit := image.Rect(-8, -12, 44, 29) // serré : l'image elle-même fait 30×20 en (5,7)
	b := New(clawdLike(), image.Point{}, limit, rand.New(rand.NewSource(4)))
	for t0 := time.Duration(0); t0 <= Duration; t0 += 20 * time.Millisecond {
		img := draw(b, t0)
		for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
			for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
				if img.NRGBAAt(x, y).A == 0 {
					continue
				}
				if !image.Pt(x, y).In(b.Bounds()) {
					t.Fatalf("à %v : (%d,%d) hors de Bounds %v", t0, x, y, b.Bounds())
				}
				if !image.Pt(x, y).In(limit) && !image.Pt(x, y).In(image.Rect(5, 7, 35, 27)) {
					t.Fatalf("à %v : (%d,%d) hors de la limite %v", t0, x, y, limit)
				}
			}
		}
	}
}

// Les pixels gardent leur couleur (pas de teinte de poussière) et leur nombre.
func TestKeepsColours(t *testing.T) {
	src := clawdLike()
	b := New(src, image.Point{}, wide, rand.New(rand.NewSource(5)))
	if want := 30 * 20; len(b.parts) != want {
		t.Fatalf("%d particules, attendu %d", len(b.parts), want)
	}
	seen := map[color.NRGBA]bool{}
	img := draw(b, 500*time.Millisecond)
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			if c := img.NRGBAAt(x, y); c.A != 0 {
				seen[c] = true
			}
		}
	}
	if len(seen) != 2 {
		t.Errorf("%d couleurs en vol, attendu les 2 de l'image : %v", len(seen), seen)
	}
}
