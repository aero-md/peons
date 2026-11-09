// gear-fx dessine les engrenages du /resume : une image PNG par position de rotation,
// dans assets/fx/gears_NN.png. Chaque image montre les deux roues, dents emboîtées ;
// l'application n'a plus qu'à enchaîner les images (voir internal/gears).
//
// Ces fichiers appartiennent à l'outil : il efface les anciens. À relancer après avoir
// touché à la forme ou à la palette, ou pour changer le nombre de poses
// (gears.Frames) :
//
//	go run ./tools/gear-fx
//	go run ./tools/gear-fx -sheet planche.png   # + une planche agrandie pour juger à l'œil
//
// Le dessin : deux roues identiques de 16×16 px, 8 dents de 2 px à flancs droits, trou
// central. Elles sont centrées sur un coin de pixel, donc exactement symétriques
// (axes et diagonales) quand une dent est sur un axe ; entre deux poses, elles tournent.
// La 2e est en haut à droite de la 1re, en sens inverse. Elles ne se recouvrent à aucun
// angle (voir les tests). Quatre couleurs au plus : un liseré, deux tons de corps et une
// lumière venue d'en haut à gauche.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"

	"peons/internal/gears"
)

var (
	root  = flag.String("assets", "assets", "dossier des assets (effets dans fx)")
	sheet = flag.String("sheet", "", "écrit en plus une planche de toutes les poses, agrandie ×10")
)

const (
	tipR      = 8.0 // rayon de la tête des dents : 16 px de large, centrée sur un coin de pixel
	rootR     = 5.8 // rayon du pied des dents (le corps)
	hubR      = 1.5 // rayon du trou central (2×2 pixels)
	toothRoot = 2.4 // largeur d'une dent au pied (pixels d'art) : 2 px sur un axe…
	toothTip  = 2.4 // …et à la tête (flancs droits)

	// Position de la 2e roue par rapport à la 1re : 14 px à droite, 4 px plus haut, soit
	// 16° (« légèrement en diagonale ») et un entraxe de 14,6, un peu plus que tipR + rootR
	// (13,8) : le jeu entre les dents. Un pas de dent = 2π / gears.Teeth.
	offX, offY = 14, 4
)

// Palette : quatre couleurs, claires, le liseré à peine plus sombre que le corps.
var (
	colEdge  = color.NRGBA{0x3b, 0x4d, 0x63, 0xff} // liseré
	colShade = color.NRGBA{0x60, 0x78, 0x91, 0xff} // corps, côté ombre (bas, droite)
	colBody  = color.NRGBA{0x8c, 0xa3, 0xb8, 0xff} // corps
	colLight = color.NRGBA{0xba, 0xcc, 0xdb, 0xff} // corps, côté lumière (haut, gauche)
)

// gear est une roue : son centre dans l'image. Les deux ont la même forme.
type gear struct {
	cx, cy float64
}

// solid dit si le point (x, y) est dans la matière de la roue tournée de theta
// (radians, sens des aiguilles d'une montre à l'écran).
func (g gear) solid(theta, x, y float64) bool {
	dx, dy := x-g.cx, y-g.cy
	r := math.Hypot(dx, dy)
	if r < hubR || r > tipR {
		return false
	}
	if r <= rootR {
		return true
	}
	// la dent la plus proche : son axe fait l'angle theta + k·(2π/Teeth)
	period := 2 * math.Pi / gears.Teeth
	a := math.Mod(math.Atan2(dy, dx)-theta, period)
	if a < 0 {
		a += period
	}
	if a > period/2 {
		a -= period
	}
	// distance au travers de la dent, et position le long de son axe : une dent a des
	// flancs droits, à peine plus étroite à la tête qu'au pied
	along, across := r*math.Cos(a), r*math.Sin(a)
	if along <= 0 {
		return false
	}
	half := (toothRoot + (toothTip-toothRoot)*(along-rootR)/(tipR-rootR)) / 2
	return math.Abs(across) <= half
}

// pair est le couple de roues, placé dans une image de size.
type pair struct {
	a, b  gear
	size  image.Point
	phase float64 // décalage de la 2e roue pour que ses dents tombent dans les creux de la 1re
}

// newPair place les deux roues : la première en bas à gauche, la seconde en haut à
// droite, d'un entraxe entier de pixels pour que chaque centre tombe sur un coin de pixel.
func newPair() *pair {
	side := int(2 * tipR) // 16 : le côté d'une roue
	p := &pair{size: image.Pt(side+offX, side+offY)}
	half := float64(side) / 2 // 8 : le coin entre quatre pixels, centre d'une roue de côté pair
	p.a = gear{cx: half, cy: half + offY}
	p.b = gear{cx: half + offX, cy: half}
	// engrènement : au point de contact, une dent de A (phase 0) fait face à un creux
	// de B (phase ½). Voir thetaB.
	phi := math.Atan2(-offY, offX) // y vers le bas : « en haut à droite » est un angle négatif
	p.phase = phi + math.Pi - (math.Pi-gears.Teeth*phi)/gears.Teeth
	return p
}

// thetaB : l'angle de la 2e roue quand la 1re est à theta. Elle tourne en sens
// inverse, du même arc (les roues sont identiques).
func (p *pair) thetaB(theta float64) float64 { return -theta + p.phase }

// pose : l'angle de la 1re roue pour la pose k, k = 0 étant une dent sur un axe.
func pose(k int) float64 { return 2 * math.Pi / gears.Teeth * float64(k) / gears.Frames }

// render rasterise les deux roues, la première tournée de theta.
func (p *pair) render(theta float64) *image.NRGBA {
	// la lumière vient d'en haut à gauche : le corps est clair de ce côté de chaque roue,
	// dans l'ombre du côté opposé, et ni l'un ni l'autre entre les deux
	tone := func(g gear, x, y int) color.NRGBA {
		t := (float64(x) + 0.5 - g.cx + float64(y) + 0.5 - g.cy) / (rootR * math.Sqrt2)
		switch {
		case t < -0.3:
			return colLight
		case t > 0.3:
			return colShade
		}
		return colBody
	}
	w, h := p.size.X, p.size.Y
	thetaB := p.thetaB(theta)
	inA := make([]bool, w*h)
	inB := make([]bool, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			fx, fy := float64(x)+0.5, float64(y)+0.5
			inA[y*w+x] = p.a.solid(theta, fx, fy)
			inB[y*w+x] = p.b.solid(thetaB, fx, fy)
		}
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	paint := func(g gear, in []bool) {
		at := func(x, y int) bool { return x >= 0 && y >= 0 && x < w && y < h && in[y*w+x] }
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				switch {
				case !at(x, y):
				case !at(x-1, y) || !at(x+1, y) || !at(x, y-1) || !at(x, y+1): // au bord de la roue (ou du trou)
					img.SetNRGBA(x, y, colEdge)
				default:
					img.SetNRGBA(x, y, tone(g, x, y))
				}
			}
		}
	}
	paint(p.a, inA)
	paint(p.b, inB)
	return img
}

func main() {
	flag.Parse()
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gear-fx :", err)
		os.Exit(1)
	}
}

func run() error {
	fx := filepath.Join(*root, "fx")
	old, _ := filepath.Glob(filepath.Join(fx, "gears_*.png"))
	for _, f := range old {
		if err := os.Remove(f); err != nil {
			return err
		}
	}
	p := newPair()
	poses := make([]*image.NRGBA, gears.Frames)
	for k := range poses {
		poses[k] = p.render(pose(k))
		if err := writePNG(filepath.Join(fx, filepath.Base(gears.File(k))), poses[k]); err != nil {
			return err
		}
	}
	fmt.Printf("%d poses de %v dans %s\n", len(poses), p.size, fx)
	if *sheet != "" {
		return writePNG(*sheet, contactSheet(poses))
	}
	return nil
}

// contactSheet : toutes les poses côte à côte, agrandies ×10, sur fond clair et sur fond sombre.
func contactSheet(poses []*image.NRGBA) *image.NRGBA {
	const scale, cols = 10, 5
	s := poses[0].Rect.Size()
	cell := image.Pt((s.X+2)*scale, (s.Y+2)*scale)
	rows := (len(poses) + cols - 1) / cols
	out := image.NewNRGBA(image.Rect(0, 0, cols*cell.X, 2*rows*cell.Y))
	for r := 0; r < 2; r++ {
		bg := color.NRGBA{0xf0, 0xf0, 0xf0, 0xff}
		if r == 1 {
			bg = color.NRGBA{0x20, 0x22, 0x2a, 0xff}
		}
		for y := r * rows * cell.Y; y < (r+1)*rows*cell.Y; y++ {
			for x := 0; x < out.Rect.Dx(); x++ {
				out.SetNRGBA(x, y, bg)
			}
		}
		for n, f := range poses {
			ox, oy := (n%cols)*cell.X+scale, (r*rows+n/cols)*cell.Y+scale
			for y := 0; y < s.Y*scale; y++ {
				for x := 0; x < s.X*scale; x++ {
					if c := f.NRGBAAt(x/scale, y/scale); c.A != 0 {
						out.SetNRGBA(ox+x, oy+y, c)
					}
				}
			}
		}
	}
	return out
}

func writePNG(name string, img image.Image) error {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return err
	}
	return os.WriteFile(name, buf.Bytes(), 0o644)
}
