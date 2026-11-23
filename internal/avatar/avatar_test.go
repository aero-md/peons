package avatar

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"peons/assets"
	"peons/internal/mood"
)

func load(t *testing.T) *Character {
	t.Helper()
	c, err := Load(assets.FS, "characters/clawd")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Le corps nettoyé + des yeux doivent redonner les sprites d'origine, hors zone
// des yeux (redessinés à la main) ; le corps endormi est retouché à la main,
// il n'a pas de source à comparer.
func TestComposeMatchesSource(t *testing.T) {
	c := load(t)
	files := map[string][]string{
		"normal": {"clawd_neutral", "clawd_neutral_1"},
		"alert":  {"clawd_alert", "clawd_alert_1"},
	}
	for body, names := range files {
		for i, name := range names {
			f, err := os.Open("../../tools/extract-eyes/source/body/" + name + ".png")
			if err != nil {
				t.Fatal(err)
			}
			img, err := png.Decode(f)
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
			want := image.NewNRGBA(img.Bounds())
			draw.Draw(want, want.Bounds(), img, img.Bounds().Min, draw.Src)
			got, err := c.Compose(body, i, "neutral")
			if err != nil {
				t.Fatal(err)
			}
			if got.Bounds() != want.Bounds() {
				t.Fatalf("%s : taille %v, attendu %v", name, got.Bounds(), want.Bounds())
			}
			// les yeux sont redessinés à la main : on ne compare que le corps autour
			var eyes image.Rectangle
			if fr := c.Bodies[body].Frames[i]; fr.Eyes != nil {
				e := c.Eyes["neutral"]
				eyes = e.Bounds().Add(eyeAt(fr.Eyes, e))
			}
			for y := 0; y < want.Rect.Dy(); y++ {
				for x := 0; x < want.Rect.Dx(); x++ {
					a, b := got.NRGBAAt(x, y), want.NRGBAAt(x, y)
					if (a.A == 0 && b.A == 0) || image.Pt(x, y).In(eyes) {
						continue
					}
					if a != b {
						t.Fatalf("%s : pixel (%d,%d) %v, attendu %v", name, x, y, a, b)
					}
				}
			}
		}
	}
}

// Toute combinaison que mood peut produire doit être dessinable, à tout instant.
func TestEveryLookIsDrawable(t *testing.T) {
	c := load(t)
	for _, look := range []mood.Look{
		{Body: mood.BodyNormal, Emotion: mood.Neutral}, {Body: mood.BodyNormal, Emotion: mood.Happy},
		{Body: mood.BodyNormal, Emotion: mood.Sad}, {Body: mood.BodyAlert, Emotion: mood.Neutral},
		{Body: mood.BodyMining, Emotion: mood.Neutral}, {Body: mood.BodyCompact, Emotion: mood.Neutral},
		{Body: mood.BodySleeping, Emotion: mood.Sleeping}, {Body: mood.BodyBench, Emotion: mood.Neutral},
	} {
		for ms := 0; ms < 8000; ms += 100 {
			el := time.Duration(ms) * time.Millisecond
			if _, err := c.Layers(string(look.Body), c.At(string(look.Body), el), string(look.Emotion)); err != nil {
				t.Errorf("%v à %v : %v", look, el, err)
			}
		}
	}
}

// Les yeux de largeur différente restent centrés sur le même axe que neutral.
func TestEyesStayCentered(t *testing.T) {
	c := load(t)
	centre := func(emotion string) int {
		ls, err := c.Layers("normal", Pose{}, emotion)
		if err != nil {
			t.Fatal(err)
		}
		eyes := ls[len(ls)-1]
		return eyes.At.X + eyes.Image.Bounds().Dx()/2
	}
	want := centre("neutral")
	for _, e := range []string{"happy", "sad"} {
		if got := centre(e); got != want {
			t.Errorf("yeux %q centrés en x=%d, neutral en x=%d", e, got, want)
		}
	}
}

// La bulle se dessine entre le corps et les yeux et boucle sur ses 3 frames ;
// les corps sans visage n'ont pas d'yeux.
func TestLayersOfRealCharacter(t *testing.T) {
	c := load(t)
	seen := map[*image.NRGBA]bool{}
	for i := 0; i < 6; i++ {
		ls, err := c.Layers("sleeping", Pose{Body: i, FX: []int{i}}, "sleeping")
		if err != nil {
			t.Fatal(err)
		}
		if len(ls) != 2 {
			t.Fatalf("frame %d : %d calques, attendu corps + bulle", i, len(ls))
		}
		seen[ls[1].Image] = true
	}
	if len(seen) != 3 {
		t.Errorf("%d images de bulle distinctes, attendu 3", len(seen))
	}
	ls, _ := c.Layers("normal", Pose{}, "neutral")
	if len(ls) != 2 || ls[1].At != image.Pt(8, 8) {
		t.Errorf("normal : %d calques, yeux en %v, attendu corps + yeux en (8,8)", len(ls), ls[len(ls)-1].At)
	}
}

// Le clawd qui creuse : deux frames à 300 ms, la roche à sa droite, la pioche dont le
// bout du manche (0,11) tombe sur sa main (15,21 puis 13,17), et le bras (calque du
// dessus, par-dessus la pioche) en (9,17) puis (7,15) ; corps, pioche et bras changent
// de frame ensemble. Yeux propres à chaque frame, non découpés (le bras couvre le débord).
func TestMiningLayout(t *testing.T) {
	c := load(t)
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }
	if c.Bodies["mining"].Period != ms(300) || len(c.Bodies["mining"].Frames) != 2 {
		t.Fatalf("mining : %d frames, période %v", len(c.Bodies["mining"].Frames), c.Bodies["mining"].Period)
	}
	hands := []image.Point{{15, 21}, {13, 17}}
	arms := []image.Point{{9, 17}, {7, 15}}
	for frame, hand := range hands {
		pose := c.At("mining", ms(300*frame))
		if pose.Body != frame || pose.FX[0] != 0 || pose.FX[1] != frame || pose.FX[2] != frame {
			t.Fatalf("à %d ms : pose %+v, attendu corps %d, roche 0, pioche %d, bras %d", 300*frame, pose, frame, frame, frame)
		}
		ls, err := c.Layers("mining", pose, "neutral")
		if err != nil {
			t.Fatal(err)
		}
		if len(ls) != 5 {
			t.Fatalf("frame %d : %d calques, attendu corps + yeux + roche + pioche + bras", frame, len(ls))
		}
		body, eyes, rock, pick, arm := ls[0], ls[1], ls[2], ls[3], ls[4]
		// yeux de profil dessinés pour chaque frame : coin haut-gauche en (15,15) puis (12,14)
		if want := []image.Point{{15, 15}, {12, 14}}[frame]; eyes.At != want {
			t.Errorf("frame %d : yeux en %v, attendus en %v", frame, eyes.At, want)
		}
		if eyes.Image == c.Eyes["neutral"] {
			t.Errorf("frame %d : les yeux du corps mining ne sont pas ceux de l'émotion", frame)
		}
		// la roche est à droite du corps (sa position exacte est un réglage du manifest)
		if rock.At.X < body.Image.Bounds().Dx()/2 {
			t.Errorf("frame %d : roche en x=%d, pas à droite du corps", frame, rock.At.X)
		}
		if got := pick.At.Add(image.Pt(0, 11)); got != hand {
			t.Errorf("frame %d : bout du manche en %v, main en %v", frame, got, hand)
		}
		// le bras est le calque du dessus (par-dessus la pioche) : il recompose le corps d'origine
		if arm.At != arms[frame] {
			t.Errorf("frame %d : bras en %v, attendu %v", frame, arm.At, arms[frame])
		}
	}
	if p := c.At("mining", ms(299)); p.Body != 0 || p.FX[1] != 0 || p.FX[2] != 0 {
		t.Errorf("à 299 ms rien ne doit avoir changé : %+v", p)
	}
}

// Le clawd qui creuse est rendu par calques : corps (sans bras), yeux (non découpés),
// roche, pioche, puis le bras par-dessus. Le corps sans bras, recouvert du bras à son
// ancrage, redonne au pixel près art/references/mining.png et mining_1.png — le
// corps d'origine, bras compris. C'est la garantie « iso » du nouveau découpage ; la
// seule différence voulue est que le bras passe désormais au-dessus de la pioche. Les
// yeux et la pioche, placés par ailleurs, sont vérifiés par TestMiningLayout.
func TestMiningMatchesReference(t *testing.T) {
	c := load(t)
	for frame, tag := range []string{"mining", "mining_1"} {
		// les références sont des artefacts d'art, hors du binaire embarqué : lues depuis art/
		ref, err := readNRGBA(os.DirFS("../../art/references"), tag+".png")
		if err != nil {
			t.Fatal(err)
		}
		ls, err := c.Layers("mining", c.At("mining", time.Duration(300*frame)*time.Millisecond), "neutral")
		if err != nil {
			t.Fatal(err)
		}
		got := image.NewNRGBA(ls[0].Image.Bounds()) // la taille du corps
		for _, l := range []Sprite{ls[0], ls[4]} {  // corps sans bras, puis bras par-dessus
			draw.Draw(got, l.Image.Bounds().Add(l.At), l.Image, image.Point{}, draw.Over)
		}
		if got.Bounds() != ref.Bounds() {
			t.Fatalf("%s : corps %v, référence %v", tag, got.Bounds(), ref.Bounds())
		}
		b := ref.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				g, w := got.NRGBAAt(x, y), ref.NRGBAAt(x, y)
				if g.A == 0 && w.A == 0 {
					continue // deux pixels transparents : égaux quelle que soit la couleur sous l'alpha
				}
				if g != w {
					t.Fatalf("%s (%d,%d) : %v, attendu %v — ancrage des yeux à revoir", tag, x, y, g, w)
				}
			}
		}
	}
}

// Le banc : corps, yeux, banc et barre composés redonnent au pixel près les deux
// planches de référence (28×26, posées en (2,6) de la toile 32×32 du corps :
// les pieds du banc sur la ligne du sol), barre en haut puis en bas à 500 ms.
func TestBenchMatchesReference(t *testing.T) {
	c := load(t)
	b := c.Bodies["bench"]
	if b == nil || len(b.Frames) != 1 || b.Frames[0].Image.Bounds() != image.Rect(0, 0, 32, 32) {
		t.Fatalf("bench : %+v, attendu une frame en toile 32×32", b)
	}
	for frame, name := range []string{"bench", "bench_1"} {
		f, err := os.Open("../../art/references/" + name + ".png")
		if err != nil {
			t.Fatal(err)
		}
		ref, err := png.Decode(f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		pose := c.At("bench", time.Duration(500*frame)*time.Millisecond)
		ls, err := c.Layers("bench", pose, "neutral")
		if err != nil {
			t.Fatal(err)
		}
		got := image.NewNRGBA(image.Rect(0, 0, 32, 32))
		for _, l := range ls {
			draw.Draw(got, l.Image.Bounds().Add(l.At), l.Image, image.Point{}, draw.Over)
		}
		at := image.Pt(2, 6)
		for y := 0; y < 32; y++ {
			for x := 0; x < 32; x++ {
				want := color.NRGBAModel.Convert(color.Transparent).(color.NRGBA)
				if p := image.Pt(x, y).Sub(at); p.In(ref.Bounds()) {
					want = color.NRGBAModel.Convert(ref.At(p.X, p.Y)).(color.NRGBA)
				}
				if want.A == 0 {
					want = color.NRGBA{}
				}
				if g := got.NRGBAAt(x, y); g != want && !(g.A == 0 && want.A == 0) {
					t.Fatalf("%s : pixel (%d,%d) = %v, attendu %v", name, x, y, g, want)
				}
			}
		}
	}
}

// Une image qui déborde de sa toile est refusée.
func TestCanvasRejectsOverflow(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 26, 16))
	if _, err := (Canvas{W: 32, H: 32, X: 7, Y: 0}).apply(src); err == nil {
		t.Error("26 px en x=7 dans 32 px : accepté")
	}
	out, err := Canvas{W: 32, H: 32, X: 3, Y: 16}.apply(src)
	if err != nil || out.Bounds() != image.Rect(0, 0, 32, 32) {
		t.Errorf("toile %v, %v", out.Bounds(), err)
	}
}

// L'écrasement retire des lignes régulièrement, jamais en haut ni en bas.
func TestSquashKeepsEdges(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 2, 12))
	for y := 0; y < 12; y++ { // chaque ligne a une valeur différente
		for x := 0; x < 2; x++ {
			src.SetNRGBA(x, y, color.NRGBA{uint8(y), 0, 0, 255})
		}
	}
	out, err := Squash{Rows: 4, KeepTop: 2, KeepBottom: 2}.apply(src)
	if err != nil {
		t.Fatal(err)
	}
	if out.Bounds().Dy() != 8 {
		t.Fatalf("hauteur %d, attendu 8", out.Bounds().Dy())
	}
	var kept []int
	for y := 0; y < out.Bounds().Dy(); y++ {
		kept = append(kept, int(out.NRGBAAt(0, y).R))
	}
	if kept[0] != 0 || kept[1] != 1 || kept[6] != 10 || kept[7] != 11 {
		t.Errorf("lignes gardées %v : les deux premières et les deux dernières doivent rester", kept)
	}
	for i := 1; i < len(kept); i++ {
		if kept[i] <= kept[i-1] {
			t.Errorf("lignes %v : l'ordre doit être conservé", kept)
		}
	}
	for _, bad := range []Squash{{Rows: 0}, {Rows: 9, KeepTop: 2, KeepBottom: 2}, {Rows: 2, KeepTop: -1}} {
		if _, err := bad.apply(src); err == nil {
			t.Errorf("%+v : une erreur est attendue", bad)
		}
	}
}

// L'écrasement préfère retirer les lignes qui répètent celle du dessus : une
// ligne qui porte un détail (un bras) n'est pas touchée.
func TestSquashRemovesRepeatedRowsFirst(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 4, 14))
	for y := 0; y < 14; y++ {
		for x := 0; x < 4; x++ {
			src.SetNRGBA(x, y, color.NRGBA{200, 100, 50, 255})
		}
	}
	src.SetNRGBA(0, 6, color.NRGBA{9, 9, 9, 255}) // un « bras » : ligne qui diffère de ses voisines
	src.SetNRGBA(0, 7, color.NRGBA{9, 9, 9, 255})
	out, err := Squash{Rows: 4, KeepTop: 1, KeepBottom: 1}.apply(src)
	if err != nil {
		t.Fatal(err)
	}
	arms := 0
	for y := 0; y < out.Bounds().Dy(); y++ {
		if out.NRGBAAt(0, y).R == 9 {
			arms++
		}
	}
	if arms != 2 {
		t.Errorf("%d lignes de bras après écrasement, attendu 2 : les lignes à détail doivent rester", arms)
	}
}

// Le piston du compactage : deux frames, le plateau pose sur le haut du clawd (à
// mi-course) puis l'écrase de 7 lignes (course complète), les pieds restant sur
// le sol. La fenêtre doit pouvoir contenir le piston entier.
func TestCompactingPiston(t *testing.T) {
	c := load(t)
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }
	if len(c.Bodies["compacting"].Frames) != 2 || c.Bodies["compacting"].Period != ms(500) {
		t.Fatalf("compacting : %d frames, période %v", len(c.Bodies["compacting"].Frames), c.Bodies["compacting"].Period)
	}
	var heights, tops [2]int
	for frame := range heights {
		ls, err := c.Layers("compacting", c.At("compacting", ms(500*frame)), "neutral")
		if err != nil {
			t.Fatal(err)
		}
		if len(ls) != 3 { // corps, yeux, piston
			t.Fatalf("frame %d : %d calques", frame, len(ls))
		}
		body, piston := ls[0], ls[2]
		heights[frame] = body.Image.Bounds().Dy()
		tops[frame] = heights[frame] - piston.At.Y
		// dernière ligne opaque du piston (le dessous du plateau) = première ligne
		// opaque du corps (sa marge transparente du haut ne compte pas)
		last, first := -1, -1
		for y := body.Image.Bounds().Dy() - 1; y >= 0; y-- {
			for x := 0; x < body.Image.Bounds().Dx(); x++ {
				if body.Image.NRGBAAt(x, y).A != 0 {
					first = y
				}
			}
		}
		for y := 0; y < piston.Image.Bounds().Dy(); y++ {
			for x := 0; x < piston.Image.Bounds().Dx(); x++ {
				if piston.Image.NRGBAAt(x, y).A != 0 {
					last = y
				}
			}
		}
		if bottom := piston.At.Y + last + 1; bottom != first {
			t.Errorf("frame %d : le dessous du plateau est en y=%d, le haut du clawd en y=%d", frame, bottom, first)
		}
	}
	if heights[0]-heights[1] != 7 {
		t.Errorf("hauteurs %v : le clawd doit perdre 7 lignes sous la course complète", heights)
	}
	// le haut du piston, au-dessus du sol : identique aux deux frames (c'est le bas qui descend)
	if tops[0] != tops[1] {
		t.Errorf("le haut du piston bouge : %v au-dessus du sol", tops)
	}
	if h := c.Height(); h < tops[0] {
		t.Errorf("hauteur utile %d : le piston entier (%d) ne tient pas", h, tops[0])
	}
}

// Le panneau d'attente : son pied (5,13) est dans la main du clawd, en (28,10) sur
// la première frame d'alert et (28,9) sur la seconde.
func TestWaitingSignInHand(t *testing.T) {
	c := load(t)
	hand := [2]image.Point{{28, 10}, {28, 9}}
	for frame := range hand {
		ls, err := c.Layers("alert", Pose{Body: frame, FX: []int{frame}}, "neutral")
		if err != nil {
			t.Fatal(err)
		}
		sign := ls[len(ls)-1]
		if got := sign.At.Add(image.Pt(5, 13)); got != hand[frame] {
			t.Errorf("frame %d : le pied du panneau est en %v, la main en %v", frame, got, hand[frame])
		}
	}
}

// pngBytes fabrique une image unie.
func pngBytes(t *testing.T, w, h int, c color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{}, draw.Src)
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// Chaque animation a sa propre période : le corps tourne à 100 ms par frame,
// l'effet à 250 ms, et l'un n'influence pas l'autre.
func TestEachAnimationHasItsOwnPace(t *testing.T) {
	fsys := fstest.MapFS{
		"c/manifest.json": {Data: []byte(`{
			"version": 1,
			"bodies": {"b": {"period": 100, "fx": ["f"], "frames": [{"image": "b0.png"}, {"image": "b1.png"}]}},
			"eyes": {},
			"fx": {"f": {"period": 250, "frames": [
				{"image": "f0.png"}, {"image": "f1.png"}, {"image": "f2.png"}]}}
		}`)},
	}
	for _, n := range []string{"c/b0", "c/b1", "fx/f0", "fx/f1", "fx/f2"} {
		fsys[n+".png"] = &fstest.MapFile{Data: pngBytes(t, 2, 2, color.NRGBA{255, 0, 0, 255})}
	}
	c, err := Load(fsys, "c")
	if err != nil {
		t.Fatal(err)
	}
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }
	tests := []struct {
		at   time.Duration
		body int
		fx   int
		next time.Duration
	}{
		{ms(0), 0, 0, ms(100)},
		{ms(99), 0, 0, ms(1)},
		{ms(100), 1, 0, ms(100)},
		{ms(240), 0, 0, ms(10)}, // l'effet change dans 10 ms, avant le corps (60 ms)
		{ms(250), 0, 1, ms(50)}, // corps : 250/100 = 2 → frame 0
		{ms(500), 1, 2, ms(100)},
		{ms(750), 1, 0, ms(50)}, // l'effet a bouclé
	}
	for _, tt := range tests {
		p := c.At("b", tt.at)
		if p.Body != tt.body || p.FX[0] != tt.fx {
			t.Errorf("à %v : corps %d, effet %d ; attendu %d, %d", tt.at, p.Body, p.FX[0], tt.body, tt.fx)
		}
		if got := c.NextChange("b", tt.at); got != tt.next {
			t.Errorf("à %v : prochain changement dans %v, attendu %v", tt.at, got, tt.next)
		}
	}
	if k := c.At("b", ms(500)).Key(); k != "1/2" {
		t.Errorf("clé %q, attendu 1/2", k)
	}
}

// Une animation à une seule frame ne bouge jamais et n'exige aucun réveil.
func TestSingleFrameNeverChanges(t *testing.T) {
	if step(time.Hour, time.Second, 1) != 0 || untilNext(time.Hour, time.Second, 1) != 0 {
		t.Error("une animation d'une frame ne doit ni changer ni réveiller")
	}
}

// Reach est exactement la zone des pixels opaques que Layers peut dessiner, pour
// tout corps, toute émotion et toute combinaison de frames, posée les pieds sur
// la ligne 0 : la fenêtre sans île s'y réduit, rien ne doit en déborder ni rester
// vide sur ses bords.
func TestReachIsWhatLayersDraw(t *testing.T) {
	c := load(t)
	var want image.Rectangle
	for name, b := range c.Bodies {
		// toutes les combinaisons : frame du corps × frame de chaque effet
		poses := []Pose{{}}
		for _, fx := range b.FX {
			var next []Pose
			for _, p := range poses {
				for i := range c.FX[fx].Frames {
					next = append(next, Pose{FX: append(append([]int(nil), p.FX...), i)})
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
				for emotion := range c.Eyes {
					ls, err := c.Layers(name, p, emotion)
					if err != nil {
						t.Fatal(err)
					}
					top := image.Pt(0, -ls[0].Image.Bounds().Dy())
					for _, l := range ls {
						lb := l.Image.Bounds()
						for y := lb.Min.Y; y < lb.Max.Y; y++ {
							for x := lb.Min.X; x < lb.Max.X; x++ {
								if l.Image.NRGBAAt(x, y).A != 0 {
									px := top.Add(l.At).Add(image.Pt(x, y))
									want = want.Union(image.Rect(px.X, px.Y, px.X+1, px.Y+1))
								}
							}
						}
					}
				}
			}
		}
	}
	if got := c.Reach(); got != want {
		t.Errorf("Reach = %v, ce que Layers dessine : %v", got, want)
	}
}

// Spacing est le plus petit écart sans contact : posés à Spacing l'un de l'autre,
// deux clawd ne se touchent pour aucune paire de poses (même en diagonale) ; un
// pixel plus près, au moins une paire se touche. Force brute sur tout ce que
// Layers peut dessiner, indépendante du calcul par lignes de Spacing. Chaque clawd
// prend n'importe quelle pose indépendamment de l'autre : une paire se touche
// exactement quand l'union de toutes les poses touche l'union décalée.
func TestSpacingIsTightestWithoutContact(t *testing.T) {
	c := load(t)
	drawn := map[image.Point]bool{} // les pixels opaques de toutes les poses, les pieds sur la ligne 0
	for name, b := range c.Bodies {
		poses := []Pose{{}}
		for _, fx := range b.FX {
			var next []Pose
			for _, p := range poses {
				for i := range c.FX[fx].Frames {
					next = append(next, Pose{FX: append(append([]int(nil), p.FX...), i)})
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
				for emotion := range c.Eyes {
					ls, err := c.Layers(name, p, emotion)
					if err != nil {
						t.Fatal(err)
					}
					top := image.Pt(0, -ls[0].Image.Bounds().Dy())
					for _, l := range ls {
						lb := l.Image.Bounds()
						for y := lb.Min.Y; y < lb.Max.Y; y++ {
							for x := lb.Min.X; x < lb.Max.X; x++ {
								if l.Image.NRGBAAt(x, y).A != 0 {
									drawn[top.Add(l.At).Add(image.Pt(x, y))] = true
								}
							}
						}
					}
				}
			}
		}
	}
	// touch : un pixel du clawd de gauche a un voisin (8 directions) dans celui de droite, décalé de s
	touch := func(left, right map[image.Point]bool, s int) bool {
		for p := range left {
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					if right[p.Add(image.Pt(dx-s, dy))] {
						return true
					}
				}
			}
		}
		return false
	}
	s := c.Spacing()
	if touch(drawn, drawn, s) {
		t.Fatalf("à %d px d'écart, deux poses se touchent", s)
	}
	if !touch(drawn, drawn, s-1) {
		t.Errorf("à %d px, aucune paire de poses ne se touche : Spacing (%d) n'est pas le plus petit écart", s-1, s)
	}
}

// Une frame d'effet a sa propre durée (ms) et peut figer le reste (hold) : pendant
// qu'elle est affichée, le corps et les autres effets s'arrêtent, puis reprennent
// là où ils en étaient ; l'effet qui fige suit le vrai temps.
func TestHoldFreezesTheRest(t *testing.T) {
	fsys := fstest.MapFS{
		"c/manifest.json": {Data: []byte(`{
			"version": 1,
			"bodies": {"b": {"period": 100, "fx": ["rock", "pick"], "frames": [{"image": "b0.png"}, {"image": "b1.png"}]}},
			"eyes": {},
			"fx": {
				"rock": {"frames": [
					{"image": "f0.png", "ms": 1000},
					{"image": "f1.png", "ms": 150, "hold": true},
					{"image": "f2.png", "ms": 50, "hold": true}]},
				"pick": {"period": 100, "frames": [{"image": "f0.png"}, {"image": "f1.png"}]}
			}
		}`)},
	}
	for _, n := range []string{"c/b0", "c/b1", "fx/f0", "fx/f1", "fx/f2"} {
		fsys[n+".png"] = &fstest.MapFile{Data: pngBytes(t, 2, 2, color.NRGBA{255, 0, 0, 255})}
	}
	c, err := Load(fsys, "c")
	if err != nil {
		t.Fatal(err)
	}
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }
	tests := []struct {
		at               time.Duration
		body, rock, pick int
		next             time.Duration
	}{
		{ms(0), 0, 0, 0, ms(100)},
		{ms(950), 1, 0, 1, ms(50)},   // 950/100 = 9 : frame 1
		{ms(1000), 0, 1, 0, ms(150)}, // figés sur la frame de 1000 ms ; seul le rocher compte
		{ms(1100), 0, 1, 0, ms(50)},  // toujours figés
		{ms(1150), 0, 2, 0, ms(50)},
		{ms(1200), 0, 0, 0, ms(100)}, // le rocher reboucle, le reste repart de 1000 ms
		{ms(1350), 1, 0, 1, ms(50)},  // horloge du reste : 1150
		{ms(2200), 0, 1, 0, ms(150)}, // second tour : figés à 2000 ms d'horloge, comme au premier
		{ms(2400), 0, 0, 0, ms(100)},
	}
	for _, tt := range tests {
		p := c.At("b", tt.at)
		if p.Body != tt.body || p.FX[0] != tt.rock || p.FX[1] != tt.pick {
			t.Errorf("à %v : corps %d, rocher %d, pioche %d ; attendu %d, %d, %d", tt.at, p.Body, p.FX[0], p.FX[1], tt.body, tt.rock, tt.pick)
		}
		if got := c.NextChange("b", tt.at); got != tt.next {
			t.Errorf("à %v : prochain changement dans %v, attendu %v", tt.at, got, tt.next)
		}
	}
}

// Deux effets qui figent le même corps, ou un effet figé en permanence : refusés.
func TestHoldIsValidated(t *testing.T) {
	for name, fx := range map[string]string{
		"deux effets qui figent": `"bodies": {"b": {"fx": ["x", "y"], "frames": [{"image": "p.png"}]}},
			"fx": {"x": {"frames": [{"image": "p.png"}, {"image": "p.png", "hold": true}]},
			       "y": {"frames": [{"image": "p.png"}, {"image": "p.png", "hold": true}]}}`,
		"toujours figé": `"bodies": {"b": {"fx": ["x"], "frames": [{"image": "p.png"}]}},
			"fx": {"x": {"frames": [{"image": "p.png", "hold": true}]}}`,
		"durée négative": `"bodies": {"b": {"fx": ["x"], "frames": [{"image": "p.png"}]}},
			"fx": {"x": {"frames": [{"image": "p.png", "ms": -5}]}}`,
	} {
		fsys := fstest.MapFS{
			"c/manifest.json": {Data: []byte(`{"version": 1, "eyes": {}, ` + fx + `}`)},
			"c/p.png":         {Data: pngBytes(t, 2, 2, color.NRGBA{255, 0, 0, 255})},
			"fx/p.png":        {Data: pngBytes(t, 2, 2, color.NRGBA{255, 0, 0, 255})},
		}
		if _, err := Load(fsys, "c"); err == nil {
			t.Errorf("%s : accepté", name)
		}
	}
}

// Dans un bloc "fx", un nom nu se lit dans FXDir à la racine, un nom avec un
// dossier (le bras du mineur, body/…) reste relatif au personnage.
func TestEffectImagesResolveFromSharedRoot(t *testing.T) {
	red, blue := color.NRGBA{255, 0, 0, 255}, color.NRGBA{0, 0, 255, 255}
	fsys := fstest.MapFS{
		"c/manifest.json": {Data: []byte(`{
			"version": 1,
			"bodies": {"b": {"fx": ["shared", "arm"], "frames": [{"image": "b0.png"}]}},
			"eyes": {},
			"fx": {
				"shared": {"frames": [{"image": "f.png"}]},
				"arm": {"frames": [{"image": "body/a.png"}]}
			}
		}`)},
		"c/b0.png":     {Data: pngBytes(t, 2, 2, red)},
		"c/body/a.png": {Data: pngBytes(t, 2, 2, blue)},
		"fx/f.png":     {Data: pngBytes(t, 2, 2, red)},
		"c/f.png":      {Data: pngBytes(t, 2, 2, blue)}, // piège : ne doit pas être lu
	}
	c, err := Load(fsys, "c")
	if err != nil {
		t.Fatal(err)
	}
	if got := c.FX["shared"].Frames[0].Image.NRGBAAt(0, 0); got != red {
		t.Errorf("effet nu : %v, attendu fx/f.png (%v)", got, red)
	}
	if got := c.FX["arm"].Frames[0].Image.NRGBAAt(0, 0); got != blue {
		t.Errorf("effet body/ : %v, attendu c/body/a.png (%v)", got, blue)
	}
}

// Le cycle de la roche du mineur (tools/rock-fx) : une minute de fissures en 5
// états, chacun sur un coup de pioche ; puis l'explosion, sur un coup aussi,
// pendant laquelle le mineur reste figé en plein coup jusqu'à ce que la roche
// neuve soit posée ; ensuite il reprend, et le cycle recommence.
func TestRockCycle(t *testing.T) {
	c := load(t)
	rock := c.FX["mining_rock"]
	var cracking time.Duration
	stages := 0
	for ; stages < len(rock.Frames) && !rock.holds(stages); stages++ {
		cracking += rock.dur(stages)
	}
	if cracking != time.Minute || stages != 5 {
		t.Fatalf("%d états de fissure en %v, attendu 5 en une minute", stages, cracking)
	}
	for i := stages; i < len(rock.Frames); i++ {
		if !rock.holds(i) {
			t.Errorf("frame %d de la roche : après l'explosion, tout doit rester figé jusqu'à la roche neuve", i)
		}
	}
	cycle := rock.cycle()
	var start time.Duration
	for i := 0; i <= stages; i++ { // chaque état, puis l'explosion (frame stages)
		if p := c.At("mining", start); p.FX[0] != i || p.Body != 0 || p.FX[1] != 0 {
			t.Errorf("à %v : roche %d, corps %d, pioche %d ; attendu roche %d sur un coup (corps et pioche 0)", start, p.FX[0], p.Body, p.FX[1], i)
		}
		if i < stages {
			start += rock.dur(i)
		}
	}
	strike := c.At("mining", cracking)
	for el := cracking; el < cycle; el += 10 * time.Millisecond {
		if p := c.At("mining", el); p.Body != strike.Body || p.FX[1] != strike.FX[1] {
			t.Fatalf("à %v, roche %d : le mineur bouge (%+v) alors qu'il doit rester figé en %+v", el, p.FX[0], p, strike)
		}
	}
	if p := c.At("mining", cycle+300*time.Millisecond); p.FX[0] != 0 || p.FX[1] == strike.FX[1] {
		t.Errorf("après la roche neuve : %+v, le mineur doit avoir repris sur une roche intacte", p)
	}
	if p := c.At("mining", cycle+cracking); p.FX[0] != stages || p.FX[1] != 0 {
		t.Errorf("second tour : %+v, attendu l'explosion sur un coup", p)
	}
	// chaque réveil annoncé change bien la pose, à travers l'explosion et la chute
	for el := cracking - time.Second; el < cycle+time.Second; {
		d := c.NextChange("mining", el)
		if d <= 0 {
			t.Fatalf("à %v : pas de prochain changement", el)
		}
		if before, after := c.At("mining", el+d-time.Millisecond).Key(), c.At("mining", el+d).Key(); before == after {
			t.Fatalf("à %v : réveil dans %v sans changement de pose (%s)", el, d, before)
		}
		el += d
	}
}

// Les frames du cycle de la roche sont tirées de assets/fx/rock.png par tools/rock-fx : les
// fissures et la chute en reprennent les pixels, les éclats et la poussière ses
// couleurs. Échoue quand rock.png a été retouché sans relancer l'outil.
func TestRockFramesFollowRockPNG(t *testing.T) {
	const rerun = "assets/fx/rock.png a changé : relancer go run ./tools/rock-fx"
	c := load(t)
	raw, err := fs.ReadFile(assets.FS, "characters/clawd/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	names := m.FX["mining_rock"].Frames
	frames := c.FX["mining_rock"].Frames
	if names[0].Image != "rock.png" {
		t.Fatalf("première frame %s, attendu la roche intacte rock.png", names[0].Image)
	}
	rock, rest := frames[0].Image, frames[0].At
	palette := map[color.NRGBA]bool{}
	b := rock.Bounds()
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			if p := rock.NRGBAAt(x, y); p.A != 0 {
				palette[p] = true
			}
		}
	}
	// each : les pixels opaques de la frame, dans le repère de la roche posée
	each := func(f Sprite, do func(q image.Point, p color.NRGBA)) {
		fb := f.Image.Bounds()
		for y := 0; y < fb.Dy(); y++ {
			for x := 0; x < fb.Dx(); x++ {
				if p := f.Image.NRGBAAt(x, y); p.A != 0 {
					do(f.At.Add(image.Pt(x, y)).Sub(rest), p)
				}
			}
		}
	}
	inRock := func(q image.Point) (color.NRGBA, bool) {
		if !q.In(b) {
			return color.NRGBA{}, false
		}
		p := rock.NRGBAAt(q.X, q.Y)
		return p, p.A != 0
	}
	for i, f := range frames {
		name := path.Base(names[i].Image)
		switch {
		case strings.HasPrefix(name, "rock_crack_"), strings.HasPrefix(name, "rock_land_"):
			// la roche posée, fendue (couleurs de la roche) ; deux éclats du bord au plus
			// ont sauté ; la poussière de l'atterrissage, autour, est de ses couleurs
			seen := 0
			each(f, func(q image.Point, p color.NRGBA) {
				if want, ok := inRock(q); ok {
					seen++
					if p != want && !palette[p] {
						t.Fatalf("%s en %v : %v, ni la roche (%v) ni l'une de ses couleurs ; %s", name, q, p, want, rerun)
					}
				} else if !palette[p] {
					t.Fatalf("%s en %v : %v n'est pas une couleur de la roche ; %s", name, q, p, rerun)
				}
			})
			opaque := 0
			each(frames[0], func(image.Point, color.NRGBA) { opaque++ })
			if seen < opaque-2 {
				t.Errorf("%s : %d pixels de la roche sur %d ; %s", name, seen, opaque, rerun)
			}
		case strings.HasPrefix(name, "rock_fall_"):
			// le bas de la roche, qui entre par le haut de la fenêtre : la ligne du bas de
			// l'image est celle de la roche, l'abscisse celle de la roche posée
			drop := f.At.Y - rest.Y - (b.Dy() - f.Image.Bounds().Dy()) // de combien la roche est au-dessus de sa place
			each(f, func(q image.Point, p color.NRGBA) {
				if want, _ := inRock(q.Sub(image.Pt(0, drop))); p != want {
					t.Fatalf("%s en %v : %v, la roche y est %v ; %s", name, q, p, want, rerun)
				}
			})
		case strings.HasPrefix(name, "rock_burst_"):
			each(f, func(q image.Point, p color.NRGBA) {
				if !palette[p] {
					t.Fatalf("%s en %v : %v n'est pas une couleur de la roche ; %s", name, q, p, rerun)
				}
			})
		case name != "rock.png":
			t.Errorf("frame %d : %s, inattendue dans le cycle de la roche", i, name)
		}
	}
}

// Le clignement suit son propre rythme : chaque clignement dure exactement Dur,
// deux débuts sont espacés de Every/2 à 3·Every/2, rien pendant la première
// demi-période, et le délai rendu tombe pile sur le changement suivant. Deux
// graines donnent deux rythmes.
func TestBlinkRhythm(t *testing.T) {
	c := load(t)
	bl := c.Bodies["normal"].Blink
	if bl == nil {
		t.Fatal("le corps normal ne cligne pas")
	}
	starts := map[uint64][]time.Duration{}
	for _, seed := range []uint64{0, 1, 42, 0xDEADBEEF} {
		var at time.Duration
		closed, left := c.Blinking("normal", "neutral", 0, seed)
		if closed {
			t.Fatalf("graine %d : yeux fermés dès le départ", seed)
		}
		for at < 10*time.Minute {
			if left <= 0 {
				t.Fatalf("graine %d à %v : aucun changement annoncé", seed, at)
			}
			if before, _ := c.Blinking("normal", "neutral", at+left-time.Nanosecond, seed); before != closed {
				t.Fatalf("graine %d : changement avant les %v annoncés à %v", seed, left, at)
			}
			at += left
			now, next := c.Blinking("normal", "neutral", at, seed)
			if now == closed {
				t.Fatalf("graine %d à %v : rien n'a changé à l'instant annoncé", seed, at)
			}
			if now {
				starts[seed] = append(starts[seed], at)
				if next != bl.Dur {
					t.Fatalf("graine %d à %v : clignement de %v, attendu %v", seed, at, next, bl.Dur)
				}
			}
			closed, left = now, next
		}
		s := starts[seed]
		if len(s) == 0 || s[0] < bl.Every/2 {
			t.Fatalf("graine %d : premiers clignements %v, attendu après %v", seed, s[:min(len(s), 3)], bl.Every/2)
		}
		for i := 1; i < len(s); i++ {
			if gap := s[i] - s[i-1]; gap < bl.Every/2 || gap >= 3*bl.Every/2 {
				t.Errorf("graine %d : %v entre deux clignements, attendu de %v à %v", seed, gap, bl.Every/2, 3*bl.Every/2)
			}
		}
	}
	if slices.Equal(starts[0], starts[1]) {
		t.Error("deux graines, un même rythme : les clawd cligneraient ensemble")
	}
}

// Seuls les yeux ouverts du clawd au repos clignent : ni les yeux déjà fermés
// (happy), ni les autres corps.
func TestBlinkOnlyIdleOpenEyes(t *testing.T) {
	c := load(t)
	blinks := func(body, emotion string) bool {
		for ms := 0; ms < 20000; ms += 10 {
			if closed, _ := c.Blinking(body, emotion, time.Duration(ms)*time.Millisecond, 7); closed {
				return true
			}
		}
		return false
	}
	for _, e := range []string{"neutral", "sad"} {
		if !blinks("normal", e) {
			t.Errorf("normal/%s ne cligne pas en 20 s", e)
		}
	}
	for _, look := range [][2]string{{"normal", "happy"}, {"alert", "neutral"}, {"mining", "neutral"},
		{"compacting", "neutral"}, {"sleeping", "sleeping"}, {"bench", "neutral"}} {
		if closed, left := c.Blinking(look[0], look[1], 3*time.Second, 7); closed || left != 0 || blinks(look[0], look[1]) {
			t.Errorf("%s/%s cligne", look[0], look[1])
		}
	}
}

// Les yeux fermés remplacent ceux de l'émotion, sur l'ancre de la frame (ils
// suivent la respiration) et centrés sur le même axe ; happy garde les siens.
func TestBlinkLayers(t *testing.T) {
	c := load(t)
	b := c.Bodies["normal"]
	open := c.Eyes["neutral"]
	for i, fr := range b.Frames {
		ls, err := c.Layers("normal", Pose{Body: i, Blink: true}, "neutral")
		if err != nil {
			t.Fatal(err)
		}
		eyes := ls[len(ls)-1]
		if eyes.Image != b.Blink.Image {
			t.Fatalf("frame %d : les yeux ne sont pas fermés", i)
		}
		if eyes.At.Y != fr.Eyes.Y || eyes.At.X+eyes.Image.Bounds().Dx()/2 != eyeAt(fr.Eyes, open).X+open.Bounds().Dx()/2 {
			t.Errorf("frame %d : yeux fermés en %v, pas sur l'axe des yeux ouverts", i, eyes.At)
		}
	}
	if ls, _ := c.Layers("normal", Pose{Blink: true}, "happy"); ls[len(ls)-1].Image != c.Eyes["happy"] {
		t.Error("happy cligne")
	}
	if (Pose{Body: 1, Blink: true}).Key() == (Pose{Body: 1}).Key() {
		t.Error("même clé, yeux ouverts ou fermés")
	}
}

// Un clignement mal décrit est refusé au chargement.
func TestBlinkIsValidated(t *testing.T) {
	for name, blink := range map[string]string{
		"sans image":        `{"ms": 100, "every": 3000}`,
		"durée nulle":       `{"image": "p.png", "ms": 0, "every": 3000}`,
		"trop rapproché":    `{"image": "p.png", "ms": 100, "every": 200}`,
		"émotion inconnue":  `{"image": "p.png", "ms": 100, "every": 3000, "emotions": ["fâché"]}`,
		"image introuvable": `{"image": "absente.png", "ms": 100, "every": 3000}`,
	} {
		fsys := fstest.MapFS{
			"c/manifest.json": {Data: []byte(`{"version": 1, "eyes": {"neutral": "p.png"}, "fx": {},
				"bodies": {"b": {"frames": [{"image": "p.png", "eyes": {"x": 1, "y": 0}}], "blink": ` + blink + `}}}`)},
			"c/p.png": {Data: pngBytes(t, 2, 2, color.NRGBA{255, 0, 0, 255})},
		}
		if _, err := Load(fsys, "c"); err == nil {
			t.Errorf("%s : accepté", name)
		}
	}
	ok := fstest.MapFS{
		"c/manifest.json": {Data: []byte(`{"version": 1, "eyes": {"neutral": "p.png"}, "fx": {},
			"bodies": {"b": {"frames": [{"image": "p.png", "eyes": {"x": 1, "y": 0}}], "blink": {"image": "p.png", "ms": 100, "every": 3000}}}}`)},
		"c/p.png": {Data: pngBytes(t, 2, 2, color.NRGBA{255, 0, 0, 255})},
	}
	if _, err := Load(ok, "c"); err != nil {
		t.Errorf("clignement valide refusé : %v", err)
	}
}
