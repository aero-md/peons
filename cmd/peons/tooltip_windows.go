package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"sync"
	"unsafe"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/sys/windows"

	"peons/assets"
)

// layeredTip : l'étiquette de survol, une petite fenêtre superposée qui laisse
// passer la souris (WS_EX_TRANSPARENT). Le texte est dessiné avec PxPlus IBM EGA
// 8x14 (assets/), à 16 px de corps × f : pour un f entier, un pixel de la police
// vaut exactement f pixels d'écran, donc le texte reste net ; pour un demi (1,5×…),
// un pixel de police tombe à cheval sur deux pixels d'écran et le texte est lissé.
type layeredTip struct {
	hwnd  uintptr
	dib   dibSection
	img   *image.RGBA // l'étiquette en RGBA prémultiplié, avant conversion en BGRA
	face  font.Face
	faceF float64
	shown bool
}

var (
	tipClass    sync.Once
	tipProc     = windows.NewCallback(tipWndProc)
	tipFontOnce sync.Once
	tipFont     *opentype.Font
	tipFontErr  error
)

const (
	wsExTransparent = 0x00000020
	wmNcHitTest     = 0x0084
	httransparent   = ^uintptr(0) // HTTRANSPARENT (-1)
)

func newTooltip(*app) tooltip { return &layeredTip{} }

func tipWndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	if msg == wmNcHitTest {
		return httransparent // la souris traverse l'étiquette
	}
	if msg == wmMouseActivate {
		return maNoActivate
	}
	r, _, _ := procDefWindowProc.Call(hwnd, msg, wparam, lparam)
	return r
}

// create crée la fenêtre, une seule fois, à la première étiquette.
func (t *layeredTip) create() error {
	if t.hwnd != 0 {
		return nil
	}
	inst, _, _ := procGetModuleHandle.Call(0)
	name, _ := windows.UTF16PtrFromString("peonsTip")
	var regErr error
	tipClass.Do(func() {
		class := wndClassEx{wndProc: tipProc, instance: inst, className: name}
		class.size = uint32(unsafe.Sizeof(class))
		if r, _, err := procRegisterClass.Call(uintptr(unsafe.Pointer(&class))); r == 0 {
			regErr = fmt.Errorf("RegisterClassEx : %w", err)
		}
	})
	if regErr != nil {
		return regErr
	}
	hwnd, _, err := procCreateWindow.Call(wsExLayered|wsExTopmost|wsExToolWindow|wsExNoActivate|wsExTransparent,
		uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(name)), wsPopup, 0, 0, 1, 1, 0, 0, inst, 0)
	if hwnd == 0 {
		return fmt.Errorf("CreateWindowEx : %w", err)
	}
	t.hwnd = hwnd
	return nil
}

// loadFace donne la police au corps de 16×f pixels (la création est coûteuse :
// elle est gardée tant que f ne change pas).
func (t *layeredTip) loadFace(f float64) (font.Face, error) {
	if t.face != nil && t.faceF == f {
		return t.face, nil
	}
	tipFontOnce.Do(func() {
		data, err := assets.FS.ReadFile("PxPlus_IBM_EGA_8x14.ttf")
		if err != nil {
			tipFontErr = err
			return
		}
		tipFont, tipFontErr = opentype.Parse(data)
	})
	if tipFontErr != nil {
		return nil, tipFontErr
	}
	face, err := opentype.NewFace(tipFont, &opentype.FaceOptions{Size: 16 * f, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		return nil, err
	}
	if t.face != nil {
		t.face.Close()
	}
	t.face, t.faceF = face, f
	return face, nil
}

func (t *layeredTip) Hide() {
	if t.hwnd != 0 && t.shown {
		procShowWindow.Call(t.hwnd, swHide)
		t.shown = false
	}
}

func (t *layeredTip) Destroy() {
	if t.hwnd != 0 {
		procDestroyWindow.Call(t.hwnd)
		t.hwnd = 0
	}
	t.dib.free()
	if t.face != nil {
		t.face.Close()
		t.face = nil
	}
}

// premul fabrique une couleur prémultipliée, comme l'attend image.RGBA.
func premul(r, g, b, a uint8) color.RGBA {
	return color.RGBA{uint8(uint16(r) * uint16(a) / 255), uint8(uint16(g) * uint16(a) / 255), uint8(uint16(b) * uint16(a) / 255), a}
}

var (
	tipBg     = premul(0x0b, 0x12, 0x20, 0xf5)
	tipBorder = premul(0x47, 0x55, 0x69, 0xff)
	tipText   = premul(0xe2, 0xe8, 0xf0, 0xff)
	tipMuted  = premul(0x94, 0xa3, 0xb8, 0xff)
	tipTrack  = premul(0x1e, 0x29, 0x3b, 0xff)
)

// barColor : vert quand il reste de la place, jaune à mi-course, rouge à la limite.
func barColor(f float64) color.RGBA {
	lerp := func(a, b uint8, t float64) uint8 { return uint8(float64(a) + (float64(b)-float64(a))*t) }
	green, yellow, red := [3]uint8{0x4a, 0xde, 0x80}, [3]uint8{0xfa, 0xcc, 0x15}, [3]uint8{0xef, 0x44, 0x44}
	from, to, t := green, yellow, f/0.5
	if f > 0.5 {
		from, to, t = yellow, red, (f-0.5)/0.5
	}
	t = max(0, min(1, t))
	return premul(lerp(from[0], to[0], t), lerp(from[1], to[1], t), lerp(from[2], to[2], t), 0xff)
}

// tipUnits : les mesures de l'étiquette pour un facteur f, en pixels d'écran. Pour
// un f entier, exactement celles d'avant les demi-pas (n×f partout) ; pour un demi,
// les longueurs sont arrondies et les traits d'un pixel de police (cadre, écart
// entre les segments) arrondis par défaut, pour rester fins.
type tipUnits struct{ f float64 }

// px : n pixels de police.
func (u tipUnits) px(n int) int { return int(math.Round(float64(n) * u.f)) }

// thin : un trait d'un pixel de police.
func (u tipUnits) thin() int { return max(1, int(u.f)) }

func (t *layeredTip) Show(info tipInfo) {
	if err := t.create(); err != nil {
		logf("étiquette : %v", err)
		return
	}
	f := max(1, info.ScaleFactor)
	face, err := t.loadFace(f)
	if err != nil {
		logf("étiquette : police : %v", err)
		return
	}
	u := tipUnits{f}
	t.compose(info, face, u)
	w, h := t.img.Rect.Dx(), t.img.Rect.Dy()
	// --- vers la fenêtre : RGBA prémultiplié -> BGRA prémultiplié
	if t.dib.w != w || t.dib.h != h {
		t.dib.create(w, h)
	}
	for i := 0; i+3 < len(t.img.Pix); i += 4 {
		t.dib.bits[i], t.dib.bits[i+1], t.dib.bits[i+2], t.dib.bits[i+3] = t.img.Pix[i+2], t.img.Pix[i+1], t.img.Pix[i], t.img.Pix[i+3]
	}
	// --- position : centrée au-dessus du bonhomme, dans l'écran
	gap := u.px(4)
	px, py := info.Anchor.Min.X+info.Anchor.Dx()/2-w/2, info.Anchor.Min.Y-h-gap
	if b, _, ok := monitorBounds(monitorFromPoint(image.Pt(info.Anchor.Min.X+info.Anchor.Dx()/2, info.Anchor.Min.Y), monitorNearest)); ok {
		px = max(b.Min.X, min(px, b.Max.X-w))
		if py < b.Min.Y { // pas de place au-dessus : sous le bonhomme
			py = info.Anchor.Max.Y + gap
		}
	}
	pt, sz, src := winPoint{int32(px), int32(py)}, winSize{int32(w), int32(h)}, winPoint{}
	blend := blendFunction{op: 0, alpha: 255, format: 1}
	procUpdateLayered.Call(t.hwnd, 0, uintptr(unsafe.Pointer(&pt)), uintptr(unsafe.Pointer(&sz)), t.dib.memDC,
		uintptr(unsafe.Pointer(&src)), 0, uintptr(unsafe.Pointer(&blend)), ulwAlpha)
	if !t.shown {
		procShowWindow.Call(t.hwnd, swShowNoActivate)
		procSetWindowPos.Call(t.hwnd, hwndTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
		t.shown = true
	}
}

// compose dessine l'étiquette dans t.img (sans fenêtre : testable).
func (t *layeredTip) compose(info tipInfo, face font.Face, u tipUnits) {
	// --- contenu
	type line struct {
		text string
		col  color.RGBA
	}
	lines := []line{{info.Name, tipText}}
	if info.Sub != "" {
		lines = append(lines, line{info.Sub, tipMuted})
	}
	lines = append(lines, line{info.State + " depuis " + info.Since, tipMuted})
	if info.Tool != "" {
		lines = append(lines, line{"outil : " + info.Tool, tipText})
	}
	if info.ContextOK {
		lines = append(lines, line{fmt.Sprintf("CONTEXTE %d %%", int(info.Fraction*100+0.5)), tipText})
	} else {
		lines = append(lines, line{"CONTEXTE inconnu", tipMuted})
	}
	var remaining string
	if info.ContextOK {
		remaining = fmt.Sprintf("%s restants sur %s", fmtTokens(info.Remaining), fmtTokens(info.WindowSize))
	}
	// --- mesures, en pixels d'écran (un pixel de police = f pixels)
	lh, padX, padY, bw := u.px(15), u.px(7), u.px(5), u.thin()
	textW := 0
	for _, l := range lines {
		textW = max(textW, font.MeasureString(face, l.text).Ceil())
	}
	textW = max(textW, font.MeasureString(face, remaining).Ceil())
	// la barre a une taille fixe, quelle que soit la largeur de l'étiquette : 24 segments
	// de 3 pixels séparés d'un pixel, dans un cadre d'un pixel
	const segments = 24
	gap := u.thin()
	seg := u.px(4) - gap // segment + écart = 4 pixels de police, tombe juste aussi pour un demi
	barW := 2*bw + segments*(seg+gap) - gap
	barH := u.px(7)
	w := 2*(bw+padX) + max(textW, barW)
	h := 2*(bw+padY) + len(lines)*lh
	if info.ContextOK {
		h += barH + u.px(2) + lh // la barre, puis les tokens restants
	}
	// --- dessin
	if t.img == nil || t.img.Rect.Dx() != w || t.img.Rect.Dy() != h {
		t.img = image.NewRGBA(image.Rect(0, 0, w, h))
	}
	fill := func(r image.Rectangle, c color.RGBA) { draw.Draw(t.img, r, &image.Uniform{c}, image.Point{}, draw.Src) }
	fill(t.img.Bounds(), tipBorder)
	fill(t.img.Bounds().Inset(bw), tipBg)
	asc := face.Metrics().Ascent.Ceil()
	text := func(x, y int, s string, c color.RGBA) {
		d := &font.Drawer{Dst: t.img, Src: &image.Uniform{c}, Face: face, Dot: fixed.P(x, y+asc)}
		d.DrawString(s)
	}
	x0, y := bw+padX, bw+padY
	for _, l := range lines {
		text(x0, y, l.text, l.col)
		y += lh
	}
	if info.ContextOK {
		// la barre « inversée » : elle se remplit avec le contexte et vire au rouge
		track := image.Rect(x0, y, x0+barW, y+barH)
		fill(track, tipBorder)
		inner := track.Inset(bw)
		fill(inner, tipTrack)
		n := segments
		filled := int(info.Fraction*float64(n) + 0.5)
		if info.Fraction > 0 {
			filled = max(1, filled)
		}
		c := barColor(info.Fraction)
		for i := 0; i < min(filled, n); i++ {
			sx := inner.Min.X + i*(seg+gap)
			fill(image.Rect(sx, inner.Min.Y, min(sx+seg, inner.Max.X), inner.Max.Y), c)
		}
		y += barH + u.px(2)
		text(x0, y, remaining, tipMuted)
	}
}
