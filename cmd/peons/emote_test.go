package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"os"
	"testing"
	"time"

	"peons/internal/prefs"
)

// Les émotes : des GIF d'animation pour Discord, Slack… Fond transparent, rognés au
// plus juste (pas un pixel vide autour), agrandis d'un facteur entier pour que le
// plus grand côté tienne dans emoteSide, sous les 128 Ko qu'acceptent Slack et Discord.
const (
	emoteSide     = 128
	emoteMaxBytes = 128 << 10
)

// emoteClip : les images d'une animation, chacune avec sa durée. Elles ont toutes la
// taille de la fenêtre, en pixels d'art.
type emoteClip struct {
	frames []*image.NRGBA
	durs   []time.Duration
}

// add ajoute l'image f, affichée pendant d ; deux images identiques de suite n'en font qu'une.
func (c *emoteClip) add(f *image.NRGBA, d time.Duration) {
	if n := len(c.frames); n > 0 && bytes.Equal(c.frames[n-1].Pix, f.Pix) {
		c.durs[n-1] += d
		return
	}
	c.frames = append(c.frames, f)
	c.durs = append(c.durs, d)
}

// write rogne le clip, l'agrandit et l'écrit dans out.
func (c *emoteClip) write(t *testing.T, out string) {
	t.Helper()
	w, h := c.frames[0].Rect.Dx(), c.frames[0].Rect.Dy()

	// la boîte qui contient tout ce qui est dessiné, sur toutes les images : le clawd
	// ne bouge pas dans le cadre d'une image à l'autre
	box := image.Rectangle{Min: image.Pt(w, h)}
	for _, f := range c.frames {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				if f.NRGBAAt(x, y).A != 0 {
					box = box.Union(image.Rect(x, y, x+1, y+1))
				}
			}
		}
	}
	if box.Empty() {
		t.Fatal("rien n'est dessiné")
	}
	if box.Min.X <= 0 || box.Min.Y <= 0 || box.Max.X >= w {
		t.Errorf("le dessin touche le bord de la toile (%v dans %dx%d) : il est coupé", box, w, h)
	}
	scale := emoteSide / max(box.Dx(), box.Dy()) // entier : pas de pixel d'art écrasé
	if scale < 1 {
		t.Fatalf("la scène (%v) ne tient pas dans %d px", box.Size(), emoteSide)
	}
	cw, ch := box.Dx()*scale, box.Dy()*scale

	pal := color.Palette{color.NRGBA{}} // index 0 : transparent
	seen := map[color.NRGBA]bool{}
	for _, f := range c.frames {
		for y := box.Min.Y; y < box.Max.Y; y++ {
			for x := box.Min.X; x < box.Max.X; x++ {
				if px := f.NRGBAAt(x, y); px.A != 0 {
					if rc := (color.NRGBA{px.R, px.G, px.B, 255}); !seen[rc] {
						seen[rc] = true
						pal = append(pal, rc)
					}
				}
			}
		}
	}
	if len(pal) > 256 {
		t.Fatalf("%d couleurs, trop pour un GIF", len(pal))
	}
	g := &gif.GIF{LoopCount: 0, BackgroundIndex: 0}
	var total time.Duration
	for i, f := range c.frames {
		p := image.NewPaletted(image.Rect(0, 0, cw, ch), pal)
		for y := 0; y < ch; y++ {
			for x := 0; x < cw; x++ {
				if px := f.NRGBAAt(box.Min.X+x/scale, box.Min.Y+y/scale); px.A != 0 {
					p.SetColorIndex(x, y, uint8(pal.Index(color.NRGBA{px.R, px.G, px.B, 255})))
				}
			}
		}
		g.Image = append(g.Image, p)
		g.Delay = append(g.Delay, int(c.durs[i]/(10*time.Millisecond)))
		g.Disposal = append(g.Disposal, gif.DisposalBackground)
		total += c.durs[i]
	}
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := gif.EncodeAll(f, g); err != nil {
		t.Fatal(err)
	}
	f.Close()
	st, _ := os.Stat(out)
	t.Logf("écrit %s : %d images, %dx%d (scène %v ×%d), boucle de %v, %d couleurs, %.1f Ko", out, len(c.frames), cw, ch, box.Size(), scale, total, len(pal)-1, float64(st.Size())/1024)
	if st.Size() > emoteMaxBytes {
		t.Errorf("%d octets : au-dessus des %d d'une émote Slack", st.Size(), emoteMaxBytes)
	}
}

// drawPose dessine dans buf le clawd du premier emplacement, dans la pose body et avec
// les yeux emotion, après elapsed d'animation, les pieds sur la ligne du sol de a.
func drawPose(t *testing.T, buf *image.NRGBA, a *app, body, emotion string, elapsed time.Duration) {
	t.Helper()
	ls, err := a.char.Layers(body, a.char.At(body, elapsed), emotion)
	if err != nil {
		t.Fatal(err)
	}
	top := a.ground/a.ui - ls[0].Image.Bounds().Dy() // les pieds sur le sol, quelle que soit la frame
	for _, l := range ls {
		r := l.Image.Bounds().Add(image.Pt(a.slotsX0+l.At.X, top+l.At.Y))
		draw.Draw(buf, r, l.Image, l.Image.Bounds().Min, draw.Over)
	}
}

// TestSleepEmote (via SLEEP_EMOTE=<gif>) rend le clawd endormi au format émote : il
// respire et la bulle de Z monte, sur le cycle complet de l'animation pour que le GIF
// reboucle sans saut.
func TestSleepEmote(t *testing.T) {
	out := os.Getenv("SLEEP_EMOTE")
	if out == "" {
		t.Skip("SLEEP_EMOTE non défini")
	}
	// le corps alterne ses 2 images toutes les 1,2 s, la bulle a 3 images de 1,2 s :
	// l'ensemble revient au départ après leur plus petit commun multiple
	const cycle = 7200 * time.Millisecond
	a := portalApp(t, prefs.Prefs{})
	pad := 8 // de la place au-dessus de la fenêtre pour la bulle ; la boîte rogne le reste
	a.ground += pad * a.ui
	w, h := a.W/a.ui, a.H/a.ui+pad

	frame := func(el time.Duration) *image.NRGBA {
		buf := image.NewNRGBA(image.Rect(0, 0, w, h))
		drawPose(t, buf, a, "sleeping", "sleeping", el)
		return buf
	}
	if !bytes.Equal(frame(0).Pix, frame(cycle).Pix) {
		t.Fatalf("l'animation ne revient pas à son début après %v", cycle)
	}
	var clip emoteClip
	for el := time.Duration(0); el < cycle; el += portalStep {
		clip.add(frame(el), portalStep)
	}
	clip.write(t, out)
}
