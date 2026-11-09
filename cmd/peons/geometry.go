package main

import (
	"image"

	"peons/internal/avatar"
	"peons/internal/terrain"
)

// spriteW : largeur d'une image de corps de clawd, en pixels d'art.
const spriteW = 32

// crowd dit comment se rangent les bonhommes, en pixels d'art. Ils sont serrés au
// plus près sans jamais se toucher : la bulle de l'un peut passer au-dessus du
// suivant, si bien que ce que dessine un bonhomme (reach) déborde de son écart.
type crowd struct {
	slot  int             // d'un corps au suivant : le plus petit écart sans contact (avatar.Spacing)
	reach image.Rectangle // ce que dessine un bonhomme, depuis le bord gauche de son corps et la ligne des pieds (avatar.Reach)
}

func crowdOf(c *avatar.Character) crowd { return crowd{slot: c.Spacing(), reach: c.Reach()} }

// geometry est la disposition horizontale de la fenêtre, en pixels d'art : où sont
// l'île et les bonhommes.
type geometry struct {
	sections int // sections centrales de l'île ; 0 : pas d'île
	islandW  int // largeur de l'île
	contentW int // largeur de ce que la fenêtre contient (île, bonhommes et leur débord)
	w        int // largeur de la fenêtre : contentW et les marges
	islandX  int // abscisse de l'île dans la fenêtre
	slotsX0  int // abscisse du premier emplacement : celle du corps du premier bonhomme
}

// span est la largeur des corps de n bonhommes côte à côte, du bord gauche du
// premier au bord droit du dernier : c'est ce que l'île doit porter.
func (c crowd) span(n int) int {
	if n <= 0 {
		return 0
	}
	return (n-1)*c.slot + spriteW
}

// autoSections donne le nombre de sections centrales pour que n bonhommes tiennent
// avec la zone de garde de l'île de chaque côté : ils ne doivent pas toucher le
// bord, qui s'incline.
func autoSections(t *terrain.Terrain, c crowd, n int) int {
	return min(maxSections, t.SectionsFor(c.span(max(n, 1))+2*t.Guard))
}

// islandGeometry dispose l'île et n bonhommes. manual > 0 impose le nombre de
// sections centrales (une île trop courte laisse alors les bonhommes déborder) ;
// 0 : automatique. Les corps des bonhommes sont centrés sur l'île ; la fenêtre
// contient l'île, tout ce que dessinent les bonhommes (la bulle du dernier, qui
// dépasse à droite), et pad pixels de marge.
func islandGeometry(t *terrain.Terrain, c crowd, n, manual, pad int) geometry {
	g := geometry{sections: manual}
	if g.sections <= 0 {
		g.sections = autoSections(t, c, n)
	}
	g.islandW = t.Width(g.sections)
	// repères relatifs au bord gauche de l'île (négatif : à gauche de l'île)
	first := (g.islandW - c.span(max(n, 1))) / 2
	left := min(0, first+c.reach.Min.X)
	right := max(g.islandW, first+(max(n, 1)-1)*c.slot+c.reach.Max.X)
	g.contentW = right - left
	g.w = g.contentW + 2*pad
	g.islandX = pad - left
	g.slotsX0 = g.islandX + first
	return g
}

// bareGeometry dispose n bonhommes sans île : la fenêtre n'est que la zone où ils
// dessinent, sans marge. Recalée dans l'écran, elle les met donc au ras des bords.
// Elle s'arrête à la ligne des pieds (voir frameFor), qui se posent ainsi sur la
// barre des tâches.
func bareGeometry(c crowd, n int) geometry {
	g := geometry{contentW: (max(n, 1)-1)*c.slot + c.reach.Dx(), slotsX0: -c.reach.Min.X}
	g.w = g.contentW
	return g
}

// frame est la disposition de la fenêtre pour un nombre d'emplacements.
type frame struct {
	g            geometry
	w, h, ground int // taille de la fenêtre et ligne des pieds, en pixels écran
	below        int // pixels d'art sous la ligne des pieds
}

// frameFor dispose la fenêtre pour n emplacements, sans y toucher.
func (a *app) frameFor(n int) frame {
	var f frame
	var above int
	// les engrenages du /resume tiennent toujours dans la fenêtre : sa hauteur ne change
	// pas quand l'animation joue (le bonhomme ne bouge pas à l'écran)
	gearTop := gearBase + a.gearAnim().Size().Y
	if a.prefs.HideIsland {
		// sans île : ce que dessinent les bonhommes, jusqu'à la ligne des pieds ; le bas
		// de la roche du mineur, qui s'enfonce d'habitude dans l'herbe, est coupé au sol
		f.g, above = bareGeometry(a.crowd, n), max(-a.crowd.reach.Min.Y, gearTop)
	} else {
		// au-dessus du sol : de quoi contenir toutes les poses (bulle, piston haut…) ;
		// les corps sont centrés sur l'île, à bonne distance de ses bords, et la fenêtre
		// contient aussi ce qui déborde (une île manuelle trop petite)
		above = max(headroom+32, a.char.Height(), gearTop)
		f.below = a.terr.Height() - a.terr.Feet // sous les pieds : le bas de l'île
		f.g = islandGeometry(a.terr, a.crowd, n, a.manualSections(), islandPad)
	}
	u := a.ui
	f.w, f.h, f.ground = f.g.w*u, (above+f.below)*u, above*u
	return f
}

// keepBodies donne l'ancre de la fenêtre (milieu du bord haut, en pixels d'écran)
// qui garde le premier corps et la ligne des pieds au même endroit de l'écran quand
// la disposition passe de before à after ; u : l'échelle.
func keepBodies(centerX, top int, before, after frame, u int) (int, int) {
	body := func(f frame) int { return f.g.slotsX0*u - f.w/2 } // le premier corps, depuis l'ancre
	return centerX + body(before) - body(after), top + before.ground - after.ground
}
