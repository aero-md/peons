// Package avatar charge un personnage fait de corps (animés), d'yeux (une image
// par émotion) et de calques d'effets (bulle de sommeil…), et expose ce qu'il
// faut dessiner. Aucune dépendance à un moteur de rendu : les backends dessinent
// les Layers.
//
// Chaque animation (un corps, un effet) a sa propre horloge et sa propre
// période : elle ne dépend que du temps écoulé depuis son démarrage, jamais d'un
// compteur partagé, donc ajouter ou retirer une animation n'accélère ni ne
// ralentit les autres. Une frame d'effet peut avoir sa propre durée (ms), et
// figer le reste (hold) : tant qu'elle est affichée, l'horloge du corps et des
// autres effets s'arrête (le mineur se fige quand sa roche explose).
//
// Ordre de dessin : corps, yeux, puis effets. Les yeux viennent de l'émotion,
// sauf frame qui nomme les siens (eyes.image dans le manifest).
//
// Un corps peut cligner des yeux (blink dans le manifest) : son horloge est à
// part, irrégulière, indépendante des frames du corps (la respiration) ; les yeux
// fermés se posent seulement sur l'ancre des yeux de la frame affichée.
//
// Convention : les pieds reposent sur la dernière ligne de l'image du corps ;
// les images d'un même personnage ont la même largeur mais pas forcément la
// même hauteur. Les coordonnées sont celles de l'image de la frame du corps
// (origine en haut à gauche) et peuvent être négatives pour un effet qui
// dépasse. Une frame sans ancre d'yeux n'en affiche pas.
package avatar

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
)

// DefaultPeriod est la durée d'une frame quand le manifest n'en précise pas.
const DefaultPeriod = 600 * time.Millisecond

// FXDir est le dossier des images d'effet (roche, pioche, banc, bulle…), à la
// racine du système de fichiers donné à Load et non dans le dossier du
// personnage : les effets sont partagés. Dans un bloc "fx" du manifest, un nom nu
// ("rock.png") se lit dans FXDir ; un nom avec un dossier ("body/mining_arm.png",
// le bras qui accompagne le corps qui creuse) reste relatif au personnage.
const FXDir = "fx"

// fxImage donne le chemin, dans le système de fichiers, d'une image d'un bloc "fx"
// du manifest du personnage de dossier dir.
func fxImage(dir, name string) string {
	if strings.Contains(name, "/") {
		return path.Join(dir, name)
	}
	return path.Join(FXDir, name)
}

type anchor struct {
	X int `json:"x"`
	Y int `json:"y"`
	// Image : yeux propres à cette frame, au lieu de ceux de l'émotion (le corps
	// qui creuse a des yeux de profil, un dessin par frame).
	Image string `json:"image"`
}

type manifest struct {
	Version int `json:"version"`
	Bodies  map[string]struct {
		Period int `json:"period"` // ms par frame
		Frames []struct {
			Image string  `json:"image"`
			Eyes  *anchor `json:"eyes"`
			// Squash : écrase l'image en retirant des lignes (le piston du compactage) ;
			// les yeux se placent dans l'image écrasée.
			Squash *Squash `json:"squash"`
			// Canvas : l'image est posée dans une toile transparente plus grande (un
			// dessin plus petit que le personnage, le corps couché sur le banc) ; les
			// coordonnées de la frame sont alors celles de la toile.
			Canvas *Canvas `json:"canvas"`
		} `json:"frames"`
		FX    []string `json:"fx"`
		Blink *struct {
			Image    string   `json:"image"`    // les yeux fermés, relatifs au personnage
			MS       int      `json:"ms"`       // durée d'un clignement
			Every    int      `json:"every"`    // intervalle moyen entre deux clignements (ms)
			Emotions []string `json:"emotions"` // émotions qui clignent ; absent : toutes
		} `json:"blink"`
	} `json:"bodies"`
	Eyes map[string]string `json:"eyes"`
	FX   map[string]struct {
		Period int `json:"period"` // ms par frame
		Frames []struct {
			Image string `json:"image"`
			X     int    `json:"x"`
			Y     int    `json:"y"`
			MS    int    `json:"ms"`   // durée de cette frame ; 0 : la période de l'effet
			Hold  bool   `json:"hold"` // tant qu'elle est affichée, le corps et les autres effets se figent
		} `json:"frames"`
	} `json:"fx"`
}

// Frame est une image de corps et l'ancre des yeux dessus : X est le centre
// horizontal de la paire d'yeux, Y son bord haut.
type Frame struct {
	Image     *image.NRGBA
	Eyes      *image.Point // nil : pas d'yeux
	EyesImage *image.NRGBA // non nil : ces yeux remplacent ceux de l'émotion
}

// Body est une silhouette animée ; FX nomme les effets qui l'accompagnent.
type Body struct {
	Frames []Frame
	Period time.Duration
	FX     []string
	Blink  *Blink // nil : ne cligne pas des yeux
	holder string // l'effet dont des frames figent les autres (voir Effect.Hold) ; vide : aucun
}

// Blink : les yeux se ferment un court instant (Dur), à intervalles irréguliers,
// sur une horloge à part. Le k-ième clignement tombe dans la seconde moitié de la
// tranche [k·Every, (k+1)·Every), à une place tirée de (seed, k) : deux
// clignements sont espacés de Every/2 à 3·Every/2, jamais au tout début de
// l'animation, et sans état à garder (la place se recalcule depuis le temps).
type Blink struct {
	Image    *image.NRGBA
	Dur      time.Duration
	Every    time.Duration
	Emotions []string // émotions qui clignent ; vide : toutes
}

// applies dit si les yeux de cette émotion clignent (pas ceux déjà fermés, ^ ^).
func (b *Blink) applies(emotion string) bool {
	return b != nil && (len(b.Emotions) == 0 || slices.Contains(b.Emotions, emotion))
}

// start : le début du k-ième clignement.
func (b *Blink) start(k int64, seed uint64) time.Duration {
	half := b.Every / 2
	j := time.Duration(mix(seed^uint64(k)*0x9E3779B97F4A7C15) % uint64(half))
	return time.Duration(k)*b.Every + half + j
}

// at dit si les yeux sont fermés après elapsed, et dans combien de temps ça change.
func (b *Blink) at(elapsed time.Duration, seed uint64) (closed bool, left time.Duration) {
	t := max(elapsed, 0)
	k := int64(t / b.Every)
	for i := max(k-1, 0); i <= k; i++ { // le précédent peut déborder sur cette tranche
		if s := b.start(i, seed); s <= t && t < s+b.Dur {
			return true, s + b.Dur - t
		}
	}
	next := b.start(k, seed)
	if next <= t {
		next = b.start(k+1, seed)
	}
	return false, next - t
}

// mix brasse 64 bits (finaliseur de splitmix64) : un tirage reproductible.
func mix(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xBF58476D1CE4E5B9
	x ^= x >> 27
	x *= 0x94D049BB133111EB
	return x ^ x>>31
}

// Sprite est une image placée par rapport au coin haut-gauche de la frame du corps.
type Sprite struct {
	Image *image.NRGBA
	At    image.Point
}

// Effect est un effet animé (bulle de sommeil…) avec son propre rythme : chaque
// frame dure Period, sauf durée propre (Times).
type Effect struct {
	Frames []Sprite
	Period time.Duration
	Times  []time.Duration // durée de chaque frame ; nil : Period pour toutes
	Hold   []bool          // frames qui figent le corps et les autres effets ; nil : aucune
}

// dur est la durée de la frame i.
func (e *Effect) dur(i int) time.Duration {
	if e.Times != nil {
		return e.Times[i]
	}
	return e.Period
}

func (e *Effect) holds(i int) bool { return e.Hold != nil && e.Hold[i] }

// cycle est la durée d'un tour complet de l'effet.
func (e *Effect) cycle() time.Duration {
	var d time.Duration
	for i := range e.Frames {
		d += e.dur(i)
	}
	return d
}

// at donne la frame affichée après elapsed, et le temps qu'il lui reste ; une
// seule frame ne change jamais (reste 0).
func (e *Effect) at(elapsed time.Duration) (frame int, left time.Duration) {
	if len(e.Frames) <= 1 {
		return 0, 0
	}
	t := max(elapsed, 0) % e.cycle()
	for i := range e.Frames {
		if t < e.dur(i) {
			return i, e.dur(i) - t
		}
		t -= e.dur(i)
	}
	return 0, e.dur(0) // inatteignable : t < cycle
}

// held donne le temps passé, depuis le démarrage, sur les frames qui figent les autres.
func (e *Effect) held(elapsed time.Duration) time.Duration {
	if e.Hold == nil || elapsed <= 0 {
		return 0
	}
	var perCycle time.Duration
	for i := range e.Frames {
		if e.holds(i) {
			perCycle += e.dur(i)
		}
	}
	cycle := e.cycle()
	h, t := elapsed/cycle*perCycle, elapsed%cycle
	for i := range e.Frames {
		if e.holds(i) {
			h += min(t, e.dur(i))
		}
		if t -= e.dur(i); t <= 0 {
			break
		}
	}
	return h
}

// Character est immuable une fois chargé.
type Character struct {
	Bodies map[string]*Body
	Eyes   map[string]*image.NRGBA
	FX     map[string]*Effect
}

// Pose est l'état d'une animation à un instant : quelle frame du corps et de
// chacun de ses effets (dans l'ordre de Body.FX), et si les yeux sont fermés
// (Blink, voir Character.Blinking : At ne le remplit pas, il dépend du bonhomme).
type Pose struct {
	Body  int
	FX    []int
	Blink bool
}

// Key identifie la pose : deux poses de même clé se dessinent à l'identique.
func (p Pose) Key() string {
	var b strings.Builder
	b.WriteString(strconv.Itoa(p.Body))
	for _, f := range p.FX {
		b.WriteByte('/')
		b.WriteString(strconv.Itoa(f))
	}
	if p.Blink {
		b.WriteByte('-')
	}
	return b.String()
}

// Load lit dir/manifest.json et les images qu'il référence. Les images des corps
// et des yeux sont relatives à dir, celles des effets à FXDir (voir fxImage).
func Load(fsys fs.FS, dir string) (*Character, error) {
	raw, err := fs.ReadFile(fsys, path.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if m.Version != 1 {
		return nil, fmt.Errorf("manifest: version %d non gérée", m.Version)
	}
	c := &Character{Bodies: map[string]*Body{}, Eyes: map[string]*image.NRGBA{}, FX: map[string]*Effect{}}
	for name, fx := range m.FX {
		e := &Effect{Period: period(fx.Period)}
		still := false // une frame au moins qui ne fige rien : sans elle, le corps ne bougerait plus
		for _, f := range fx.Frames {
			img, err := readNRGBA(fsys, fxImage(dir, f.Image))
			if err != nil {
				return nil, err
			}
			if f.MS < 0 {
				return nil, fmt.Errorf("effet %q, %s : durée %d ms négative", name, f.Image, f.MS)
			}
			e.Frames = append(e.Frames, Sprite{img, image.Pt(f.X, f.Y)})
			e.Times = append(e.Times, e.Period)
			if f.MS > 0 {
				e.Times[len(e.Times)-1] = time.Duration(f.MS) * time.Millisecond
			}
			e.Hold = append(e.Hold, f.Hold)
			still = still || !f.Hold
		}
		if len(e.Frames) == 0 {
			return nil, fmt.Errorf("effet %q sans frame", name)
		}
		if !still {
			return nil, fmt.Errorf("effet %q : toutes ses frames figent le corps", name)
		}
		c.FX[name] = e
	}
	for name, b := range m.Bodies {
		if len(b.Frames) == 0 {
			return nil, fmt.Errorf("corps %q sans frame", name)
		}
		body := &Body{FX: b.FX, Period: period(b.Period)}
		for _, fx := range b.FX {
			e, ok := c.FX[fx]
			if !ok {
				return nil, fmt.Errorf("corps %q : effet %q inconnu", name, fx)
			}
			if slices.Contains(e.Hold, true) {
				if body.holder != "" {
					return nil, fmt.Errorf("corps %q : %q et %q figent tous deux le corps, un seul le peut", name, body.holder, fx)
				}
				body.holder = fx
			}
		}
		for _, f := range b.Frames {
			img, err := readNRGBA(fsys, path.Join(dir, f.Image))
			if err != nil {
				return nil, err
			}
			if f.Canvas != nil {
				if img, err = f.Canvas.apply(img); err != nil {
					return nil, fmt.Errorf("corps %q, %s : %w", name, f.Image, err)
				}
			}
			fr := Frame{Image: img}
			if f.Squash != nil {
				if fr.Image, err = f.Squash.apply(img); err != nil {
					return nil, fmt.Errorf("corps %q, %s : %w", name, f.Image, err)
				}
			}
			if f.Eyes != nil {
				fr.Eyes = &image.Point{f.Eyes.X, f.Eyes.Y}
				if f.Eyes.Image != "" {
					if fr.EyesImage, err = readNRGBA(fsys, path.Join(dir, f.Eyes.Image)); err != nil {
						return nil, err
					}
				}
			}
			body.Frames = append(body.Frames, fr)
		}
		if bl := b.Blink; bl != nil {
			// deux clignements ne se chevauchent jamais : Dur < Every/2 (voir Blink)
			if bl.Image == "" || bl.MS <= 0 || bl.Every <= 2*bl.MS {
				return nil, fmt.Errorf("corps %q : clignement invalide (image %q, %d ms toutes les %d ms : il faut every > 2×ms)", name, bl.Image, bl.MS, bl.Every)
			}
			img, err := readNRGBA(fsys, path.Join(dir, bl.Image))
			if err != nil {
				return nil, err
			}
			body.Blink = &Blink{Image: img, Dur: time.Duration(bl.MS) * time.Millisecond, Every: time.Duration(bl.Every) * time.Millisecond, Emotions: bl.Emotions}
		}
		c.Bodies[name] = body
	}
	for name, file := range m.Eyes {
		img, err := readNRGBA(fsys, path.Join(dir, file))
		if err != nil {
			return nil, err
		}
		c.Eyes[name] = img
	}
	for name, b := range c.Bodies {
		for _, e := range b.Blink.emotions() {
			if _, ok := c.Eyes[e]; !ok {
				return nil, fmt.Errorf("corps %q : le clignement nomme l'émotion %q, inconnue", name, e)
			}
		}
	}
	return c, nil
}

// emotions : celles que nomme le clignement (nil sans clignement).
func (b *Blink) emotions() []string {
	if b == nil {
		return nil
	}
	return b.Emotions
}

func period(ms int) time.Duration {
	if ms <= 0 {
		return DefaultPeriod
	}
	return time.Duration(ms) * time.Millisecond
}

// At donne la pose du corps après elapsed depuis le démarrage de l'animation.
func (c *Character) At(body string, elapsed time.Duration) Pose {
	b, ok := c.Bodies[body]
	if !ok {
		return Pose{}
	}
	clock := c.clock(b, elapsed)
	p := Pose{Body: step(clock, b.Period, len(b.Frames))}
	for _, name := range b.FX {
		t := clock
		if name == b.holder { // l'effet qui fige les autres suit le vrai temps
			t = elapsed
		}
		f, _ := c.FX[name].at(t)
		p.FX = append(p.FX, f)
	}
	return p
}

// clock : le temps vu par le corps et les effets qui ne le figent pas, après
// elapsed de vrai temps : il ne compte pas les frames qui les figent.
func (c *Character) clock(b *Body, elapsed time.Duration) time.Duration {
	if b.holder == "" {
		return elapsed
	}
	return elapsed - c.FX[b.holder].held(elapsed)
}

// NextChange donne le délai avant que la pose change (corps ou effet) : une
// boucle d'affichage événementielle peut dormir jusque-là.
func (c *Character) NextChange(body string, elapsed time.Duration) time.Duration {
	b, ok := c.Bodies[body]
	if !ok {
		return 0
	}
	var next time.Duration
	soonest := func(d time.Duration) {
		if d > 0 && (next == 0 || d < next) {
			next = d
		}
	}
	frozen := false
	if b.holder != "" {
		h := c.FX[b.holder]
		f, left := h.at(elapsed)
		soonest(left)
		frozen = h.holds(f) // figés : rien d'autre ne bouge avant la fin de cette frame
	}
	if frozen {
		return next
	}
	clock := c.clock(b, elapsed)
	soonest(untilNext(clock, b.Period, len(b.Frames)))
	for _, name := range b.FX {
		if name != b.holder {
			_, left := c.FX[name].at(clock)
			soonest(left)
		}
	}
	return next
}

// Blinking dit si les yeux sont fermés (clignement) après elapsed depuis le
// démarrage de l'animation du corps, et dans combien de temps ça change (0 :
// jamais, ce corps ou cette émotion ne clignent pas). seed est propre au
// bonhomme : deux clawd côte à côte ne clignent pas ensemble. À reporter dans
// Pose.Blink.
func (c *Character) Blinking(body, emotion string, elapsed time.Duration, seed uint64) (closed bool, left time.Duration) {
	b, ok := c.Bodies[body]
	if !ok || !b.Blink.applies(emotion) {
		return false, 0
	}
	return b.Blink.at(elapsed, seed)
}

// step : index de frame après elapsed ; n == 1 ne bouge jamais.
func step(elapsed, period time.Duration, n int) int {
	if n <= 1 || elapsed < 0 {
		return 0
	}
	return int(elapsed/period) % n
}

// untilNext : délai avant le prochain changement de frame, 0 si l'animation ne bouge pas.
func untilNext(elapsed, period time.Duration, n int) time.Duration {
	if n <= 1 {
		return 0
	}
	return period - elapsed%period
}

// Canvas pose l'image en (X,Y) d'une toile transparente W×H : la dernière ligne
// de la toile est celle du sol.
type Canvas struct {
	W int `json:"w"`
	H int `json:"h"`
	X int `json:"x"`
	Y int `json:"y"`
}

func (c Canvas) apply(src *image.NRGBA) (*image.NRGBA, error) {
	at := src.Bounds().Sub(src.Bounds().Min).Add(image.Pt(c.X, c.Y))
	out := image.NewNRGBA(image.Rect(0, 0, c.W, c.H))
	if c.X < 0 || c.Y < 0 || !at.In(out.Bounds()) {
		return nil, fmt.Errorf("image %dx%d en (%d,%d) hors de la toile %dx%d", at.Dx(), at.Dy(), c.X, c.Y, c.W, c.H)
	}
	draw.Draw(out, at, src, src.Bounds().Min, draw.Src)
	return out, nil
}

// Squash décrit un écrasement vertical : on retire Rows lignes, réparties
// régulièrement parmi celles qui répètent la ligne du dessus (aplats : le corps
// perd de la hauteur sans que les bras ni les détails soient abîmés ; à défaut
// de lignes répétées en nombre suffisant, parmi toutes), mais jamais les KeepTop premières lignes du dessin (son contour
// du haut, la marge transparente au-dessus ne compte pas) ni les KeepBottom
// dernières (les pattes, qui doivent rester entières). La marge transparente du
// haut n'est jamais touchée : le dessin écrasé garde la même hauteur de marge.
type Squash struct {
	Rows       int `json:"rows"`
	KeepTop    int `json:"keep_top"`
	KeepBottom int `json:"keep_bottom"`
}

// apply rend l'image écrasée, de hauteur Dy-Rows : les pieds restent sur la
// dernière ligne.
func (s Squash) apply(src *image.NRGBA) (*image.NRGBA, error) {
	b := src.Bounds()
	h := b.Dy()
	margin := topMargin(src)
	keepTop := margin + s.KeepTop
	middle := h - keepTop - s.KeepBottom
	if s.Rows <= 0 || s.KeepTop < 0 || s.KeepBottom < 0 || s.Rows >= middle {
		return nil, fmt.Errorf("écrasement de %d lignes impossible sur %d lignes (%d gardées en haut, %d en bas)", s.Rows, h, s.KeepTop, s.KeepBottom)
	}
	row := func(y int) []byte { return src.Pix[y*src.Stride : y*src.Stride+4*b.Dx()] }
	var candidates []int // lignes du milieu identiques à celle du dessus
	for y := keepTop; y < h-s.KeepBottom; y++ {
		if y > 0 && bytes.Equal(row(y), row(y-1)) {
			candidates = append(candidates, y)
		}
	}
	if len(candidates) < s.Rows {
		candidates = candidates[:0]
		for y := keepTop; y < h-s.KeepBottom; y++ {
			candidates = append(candidates, y)
		}
	}
	drop := make(map[int]bool, s.Rows)
	for k := 0; k < s.Rows; k++ {
		// le milieu de chaque tranche de len(candidates)/Rows candidates
		drop[candidates[(2*k+1)*len(candidates)/(2*s.Rows)]] = true
	}
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), h-s.Rows))
	y := 0
	for sy := 0; sy < h; sy++ {
		if drop[sy] {
			continue
		}
		copy(out.Pix[y*out.Stride:(y+1)*out.Stride], src.Pix[(sy)*src.Stride:(sy)*src.Stride+out.Stride])
		y++
	}
	return out, nil
}

// topMargin compte les lignes entièrement transparentes en haut de l'image.
func topMargin(img *image.NRGBA) int {
	b := img.Bounds()
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			if img.Pix[y*img.Stride+4*x+3] != 0 {
				return y
			}
		}
	}
	return b.Dy()
}

// Height donne la hauteur au-dessus de la ligne du sol de tout ce que le
// personnage peut dessiner, toutes poses confondues : la fenêtre doit au moins
// la contenir.
func (c *Character) Height() int {
	h := 0
	for _, b := range c.Bodies {
		for _, fr := range b.Frames {
			h = max(h, fr.Image.Bounds().Dy())
			for _, name := range b.FX {
				for _, f := range c.FX[name].Frames {
					h = max(h, fr.Image.Bounds().Dy()-f.At.Y)
				}
			}
		}
	}
	return h
}

// eachPlaced appelle f pour chaque image que le personnage peut dessiner, toutes
// poses, émotions et effets confondus, avec sa position : x depuis le bord gauche
// de l'image du corps, y depuis la ligne du sol (négatif au-dessus). Chaque frame
// d'effet peut accompagner chaque frame du corps : leurs horloges sont distinctes.
func (c *Character) eachPlaced(f func(img *image.NRGBA, at image.Point)) {
	for _, b := range c.Bodies {
		for _, fr := range b.Frames {
			top := image.Pt(0, -fr.Image.Bounds().Dy()) // le coin haut-gauche du corps, les pieds sur la ligne 0
			f(fr.Image, top)
			if fr.Eyes != nil {
				eyes := []*image.NRGBA{fr.EyesImage}
				if fr.EyesImage == nil {
					eyes = eyes[:0]
					for _, e := range c.Eyes {
						eyes = append(eyes, e)
					}
					if b.Blink != nil {
						eyes = append(eyes, b.Blink.Image)
					}
				}
				for _, e := range eyes {
					f(e, top.Add(eyeAt(fr.Eyes, e)))
				}
			}
			for _, name := range b.FX {
				for _, fx := range c.FX[name].Frames {
					f(fx.Image, top.Add(fx.At))
				}
			}
		}
	}
}

// Reach donne le rectangle des pixels opaques que le personnage peut dessiner,
// toutes poses, émotions et effets confondus, dans le repère de eachPlaced. Rien
// de dessiné : le rectangle vide.
func (c *Character) Reach() image.Rectangle {
	var r image.Rectangle
	c.eachPlaced(func(img *image.NRGBA, at image.Point) {
		if o := opaqueBounds(img); !o.Empty() {
			r = r.Union(o.Add(at))
		}
	})
	return r
}

// Spacing donne le plus petit écart, du bord gauche d'un corps à celui du suivant,
// qui laisse au moins un pixel vide entre deux personnages côte à côte quelles que
// soient leurs poses (la bulle de sommeil de l'un, le banc ou la roche de l'autre) :
// aucun pixel de l'un ne touche un pixel de l'autre, même en diagonale. Calculé
// ligne par ligne, pas sur les rectangles : une bulle en hauteur peut passer
// au-dessus de la roche du voisin.
func (c *Character) Spacing() int {
	left, right := map[int]int{}, map[int]int{} // par ligne : le pixel opaque le plus à gauche, le plus à droite
	c.eachPlaced(func(img *image.NRGBA, at image.Point) {
		b := img.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				if img.NRGBAAt(x, y).A == 0 {
					continue
				}
				py, px := at.Y+y, at.X+x
				if l, ok := left[py]; !ok || px < l {
					left[py] = px
				}
				if r, ok := right[py]; !ok || px > r {
					right[py] = px
				}
			}
		}
	})
	s := 1
	for y, r := range right {
		for dy := -1; dy <= 1; dy++ { // la ligne et ses deux voisines : pas de contact en diagonale
			if l, ok := left[y+dy]; ok {
				s = max(s, r-l+2) // le voisin commence au plus tôt en r+2 : la colonne r+1 reste vide
			}
		}
	}
	return s
}

// opaqueBounds : le plus petit rectangle qui contient les pixels non transparents.
func opaqueBounds(img *image.NRGBA) image.Rectangle {
	var r image.Rectangle
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.NRGBAAt(x, y).A != 0 {
				r = r.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return r
}

// eyesOf donne les yeux d'une frame : les siens s'ils sont dessinés pour elle,
// sinon ceux de l'émotion, fermés si blink (et que le corps cligne pour cette
// émotion). nil si la frame n'a pas d'yeux.
func (c *Character) eyesOf(b *Body, fr Frame, emotion string, blink bool) (*image.NRGBA, error) {
	if fr.Eyes == nil {
		return nil, nil
	}
	if fr.EyesImage != nil {
		return fr.EyesImage, nil
	}
	eyes, ok := c.Eyes[emotion]
	if !ok {
		return nil, fmt.Errorf("émotion %q inconnue", emotion)
	}
	if blink && b.Blink.applies(emotion) {
		return b.Blink.Image, nil
	}
	return eyes, nil
}

// Layers donne, dans l'ordre de dessin, tout ce qui compose la pose : corps,
// yeux, puis effets (la pioche passe ainsi devant le visage). Le premier calque
// est le corps, en (0,0) : ses dimensions donnent la ligne du sol.
func (c *Character) Layers(body string, p Pose, emotion string) ([]Sprite, error) {
	b, ok := c.Bodies[body]
	if !ok {
		return nil, fmt.Errorf("corps %q inconnu", body)
	}
	fr := b.Frames[mod(p.Body, len(b.Frames))]
	out := []Sprite{{fr.Image, image.Point{}}}
	eyes, err := c.eyesOf(b, fr, emotion, p.Blink)
	if err != nil {
		return nil, err
	}
	if eyes != nil {
		out = append(out, Sprite{eyes, eyeAt(fr.Eyes, eyes)})
	}
	for i, name := range b.FX {
		fx := c.FX[name]
		out = append(out, fx.Frames[mod(p.FX[i], len(fx.Frames))])
	}
	return out, nil
}

// AllSprites donne toutes les images que le corps peut dessiner, toutes frames
// confondues : de quoi calculer une zone qui ne change pas pendant l'animation.
func (c *Character) AllSprites(body, emotion string) []Sprite {
	b, ok := c.Bodies[body]
	if !ok {
		return nil
	}
	var out []Sprite
	for _, fr := range b.Frames {
		out = append(out, Sprite{fr.Image, image.Point{}})
		if eyes, err := c.eyesOf(b, fr, emotion, false); err == nil && eyes != nil {
			out = append(out, Sprite{eyes, eyeAt(fr.Eyes, eyes)})
			if fr.EyesImage == nil && b.Blink.applies(emotion) { // et fermés
				out = append(out, Sprite{b.Blink.Image, eyeAt(fr.Eyes, b.Blink.Image)})
			}
		}
	}
	for _, name := range b.FX {
		out = append(out, c.FX[name].Frames...)
	}
	return out
}

// eyeAt : l'ancre est le centre horizontal de la paire, des yeux de largeurs
// différentes restent donc centrés.
func eyeAt(anchor *image.Point, eyes *image.NRGBA) image.Point {
	return image.Pt(anchor.X-eyes.Bounds().Dx()/2, anchor.Y)
}

// Compose dessine le corps et ses yeux (sans effets) dans une image aux
// dimensions du corps : c'est la référence pour vérifier l'extraction.
func (c *Character) Compose(body string, frame int, emotion string) (*image.NRGBA, error) {
	b, ok := c.Bodies[body]
	if !ok {
		return nil, fmt.Errorf("corps %q inconnu", body)
	}
	fr := b.Frames[mod(frame, len(b.Frames))]
	out := image.NewNRGBA(fr.Image.Bounds())
	draw.Draw(out, out.Bounds(), fr.Image, image.Point{}, draw.Src)
	eyes, err := c.eyesOf(b, fr, emotion, false)
	if err != nil {
		return nil, err
	}
	if eyes != nil {
		draw.Draw(out, eyes.Bounds().Add(eyeAt(fr.Eyes, eyes)), eyes, image.Point{}, draw.Over)
	}
	return out, nil
}

func mod(i, n int) int { return (i%n + n) % n }

// LoadImage lit une image PNG en NRGBA : un sprite autonome, hors manifest (les
// particules de sous-agents au-dessus du clawd).
func LoadImage(fsys fs.FS, name string) (*image.NRGBA, error) { return readNRGBA(fsys, name) }

func readNRGBA(fsys fs.FS, name string) (*image.NRGBA, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	out := image.NewNRGBA(img.Bounds())
	draw.Draw(out, out.Bounds(), img, img.Bounds().Min, draw.Src)
	return out, nil
}
