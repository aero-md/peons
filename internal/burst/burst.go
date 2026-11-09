// Package burst fait exploser une image puis la reconstitue : chaque pixel est
// projeté de son centre vers l'extérieur, plus ou moins loin et pas tout à fait dans
// la même direction que son voisin, ralentit jusqu'à s'arrêter, attend un instant,
// puis revient à sa place, les uns après les autres, jusqu'à refaire l'image.
//
// C'est l'animation du /clear, cousine de snap (la désagrégation d'un clawd qui
// s'en va) mais sans vent ni disparition : rien ne se perd, tout revient.
//
// Comme les autres animations, c'est une fonction du temps : Draw dessine l'état à
// l'instant t, sans rien retenir d'un appel à l'autre.
package burst

import (
	"image"
	"math"
	"math/rand"
	"time"
)

// Duration : à la fin, l'image est reconstituée à l'identique.
const Duration = 1200 * time.Millisecond

const (
	outFor   = 0.40 // secondes : la projection (qui ralentit jusqu'à l'arrêt)
	holdTill = 0.48 // le premier pixel repart à cet instant (une brève pause après l'arrêt)…
	spread   = 0.20 // …le dernier, spread plus tard (ils ne reviennent pas tous d'un coup)
	backFor  = 0.50 // durée du retour d'un pixel

	minDist = 3.0  // distance de projection (pixels d'art), au plus près…
	maxDist = 11.0 // …et au plus loin
	cone    = 1.0  // dispersion de la direction autour de la radiale (radians, ± cone)
)

type particle struct {
	home   image.Point // sa place dans l'image
	col    [4]uint8    // NRGBA
	dx, dy float64     // déplacement au plus loin, déjà borné à limit
	delay  float64     // instant où il repart
}

// Burst est l'explosion d'une image ; immuable une fois créée.
type Burst struct {
	parts  []particle
	bounds image.Rectangle
}

// New prépare l'explosion des pixels opaques de img, posée en at. Aucun pixel ne
// sort de limit (même repère que at) : la fenêtre est finie, et le sol aussi.
func New(img *image.NRGBA, at image.Point, limit image.Rectangle, rng *rand.Rand) *Burst {
	b := img.Bounds()
	var opaque image.Rectangle
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.NRGBAAt(x, y).A != 0 {
				opaque = opaque.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	cx := float64(opaque.Min.X+opaque.Max.X-1) / 2
	cy := float64(opaque.Min.Y+opaque.Max.Y-1) / 2
	s := &Burst{}
	for y := opaque.Min.Y; y < opaque.Max.Y; y++ {
		for x := opaque.Min.X; x < opaque.Max.X; x++ {
			c := img.NRGBAAt(x, y)
			if c.A == 0 {
				continue
			}
			home := image.Pt(x, y).Sub(b.Min).Add(at)
			// la direction part du centre de l'image, de travers d'au plus cone
			angle := math.Atan2(float64(y)-cy, float64(x)-cx)
			if math.Hypot(float64(x)-cx, float64(y)-cy) < 1.5 { // au centre, pas de « dehors » : n'importe où
				angle = rng.Float64() * 2 * math.Pi
			}
			angle += (rng.Float64()*2 - 1) * cone
			dist := minDist + (maxDist-minDist)*math.Pow(rng.Float64(), 0.8)
			p := particle{
				home:  home,
				col:   [4]uint8{c.R, c.G, c.B, c.A},
				dx:    math.Cos(angle) * dist,
				dy:    math.Sin(angle) * dist,
				delay: holdTill + spread*rng.Float64(),
			}
			p.clamp(limit)
			s.parts = append(s.parts, p)
		}
	}
	for _, p := range s.parts { // tout ce que Draw peut toucher
		s.bounds = s.bounds.Union(image.Rect(p.home.X, p.home.Y, p.home.X+1, p.home.Y+1))
		q := p.peak()
		s.bounds = s.bounds.Union(image.Rect(q.X, q.Y, q.X+1, q.Y+1))
	}
	return s
}

// peak : où le pixel s'arrête, au plus loin.
func (p particle) peak() image.Point {
	return image.Pt(p.home.X+int(math.Round(p.dx)), p.home.Y+int(math.Round(p.dy)))
}

// clamp ramène le déplacement pour que le pixel, au plus loin, reste dans limit : ce
// qui dépasserait rebondit sur le bord (le sol, la fenêtre) au lieu de s'y écraser en
// ligne. La place d'origine n'est jamais touchée : hors limit, le pixel ne fait que
// revenir vers elle.
func (p *particle) clamp(limit image.Rectangle) {
	fx := func(v float64, home, lo, hi int) float64 {
		at, first, last := float64(home)+v, float64(lo), float64(hi-1)
		if at > last {
			at = 2*last - at
		}
		if at < first {
			at = 2*first - at
		}
		return math.Max(first, math.Min(last, at)) - float64(home)
	}
	p.dx = fx(p.dx, p.home.X, min(limit.Min.X, p.home.X), max(limit.Max.X, p.home.X+1))
	p.dy = fx(p.dy, p.home.Y, min(limit.Min.Y, p.home.Y), max(limit.Max.Y, p.home.Y+1))
}

// out : la projection, de 0 à 1, qui ralentit jusqu'à s'arrêter (sortie cubique).
func out(t float64) float64 {
	u := math.Min(1, t/outFor)
	return 1 - (1-u)*(1-u)*(1-u)
}

// back : le retour, de 0 (parti) à 1 (revenu), qui accélère puis freine (entrée-sortie cubique).
func back(t float64) float64 {
	u := math.Max(0, math.Min(1, t/backFor))
	if u < 0.5 {
		return 4 * u * u * u
	}
	return 1 - math.Pow(-2*u+2, 3)/2
}

// pos : la place du pixel à l'instant t (secondes).
func (p particle) pos(t float64) image.Point {
	k := out(t) * (1 - back(t-p.delay)) // 0 : chez lui ; 1 : au plus loin
	return image.Pt(p.home.X+int(math.Round(p.dx*k)), p.home.Y+int(math.Round(p.dy*k)))
}

// Draw dessine l'explosion à l'instant t dans dst, décalée de origin ; ce qui sort
// de dst est perdu. Les pixels sont posés dans l'ordre de l'image : quand deux
// tombent au même endroit, le dernier gagne.
func (b *Burst) Draw(dst *image.NRGBA, origin image.Point, t time.Duration) {
	sec := t.Seconds()
	for _, p := range b.parts {
		if q := p.pos(sec).Add(origin); q.In(dst.Rect) {
			i := dst.PixOffset(q.X, q.Y)
			copy(dst.Pix[i:i+4], p.col[:])
		}
	}
}

// Bounds : tout ce que Draw peut toucher, avec origin en (0,0).
func (b *Burst) Bounds() image.Rectangle { return b.bounds }
