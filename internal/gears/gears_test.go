package gears

import (
	"math"
	"testing"
	"time"

	"peons/assets"
)

func TestLoadEmbeddedAssets(t *testing.T) {
	an, err := Load(assets.FS)
	if err != nil {
		t.Fatal(err)
	}
	if len(an.Frames) != Frames {
		t.Fatalf("%d images, attendu %d", len(an.Frames), Frames)
	}
	if s := an.Size(); s.X <= 0 || s.Y <= 0 {
		t.Errorf("taille %v", s)
	}
	if File(0) != "fx/gears_01.png" || File(Frames-1) != "fx/gears_10.png" {
		t.Errorf("noms : %s … %s", File(0), File(Frames-1))
	}
}

func TestAnimationTimeline(t *testing.T) {
	an, err := Load(assets.FS)
	if err != nil {
		t.Fatal(err)
	}
	if k, ok := an.Frame(0); !ok || k != 0 {
		t.Errorf("au départ : pose %d %v, attendu la 0", k, ok)
	}
	if _, ok := an.Frame(Duration); ok {
		t.Error("l'animation doit être finie à Duration")
	}
	if _, ok := an.Frame(-time.Millisecond); ok {
		t.Error("pas d'image avant le début")
	}
	if _, ok := an.Frame(Duration - time.Millisecond); !ok {
		t.Error("dernière milliseconde : l'animation tourne encore")
	}
	// la vitesse monte, tient, retombe à zéro
	if speed(0) != 0 || speed(Duration) != 0 || speed(Duration/2) != 1 {
		t.Errorf("vitesse %v / %v / %v : attendu 0, 1, 0", speed(0), speed(Duration/2), speed(Duration))
	}
	if angleAt(Duration) <= angleAt(Duration/2) || angleAt(Duration/2) <= 0 {
		t.Error("l'angle doit croître")
	}
	if NextFrame(0) != Step || NextFrame(Step-1) != Step || NextFrame(Step) != 2*Step {
		t.Error("NextFrame ne tombe pas sur la prochaine image")
	}
}

// Toutes les poses servent, dans l'ordre, et d'un pas à l'autre on avance de moins d'une
// demi-boucle : au-delà, l'œil croit voir tourner à l'envers.
func TestFramesFollowTheRotation(t *testing.T) {
	an, err := Load(assets.FS)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int]bool{}
	worst := 0
	prev, _ := an.Frame(0)
	for el := Step; el < Duration; el += Step {
		k, ok := an.Frame(el)
		if !ok || k < 0 || k >= Frames {
			t.Fatalf("à %v : pose %d %v", el, k, ok)
		}
		seen[k] = true
		worst = max(worst, (k-prev+Frames)%Frames)
		prev = k
	}
	if len(seen) != Frames {
		t.Errorf("%d poses utilisées sur %d", len(seen), Frames)
	}
	if worst >= Frames/2 {
		t.Errorf("jusqu'à %d poses d'un pas à l'autre : au-delà de %d, la rotation paraît inversée", worst, Frames/2-1)
	}
	// à pleine vitesse : 9° par pas, soit 0,2 pas de dent
	if step := angleAt(Duration/2+Step) - angleAt(Duration/2); math.Abs(step-2*math.Pi*revPerSec*Step.Seconds()) > 1e-6 {
		t.Errorf("%.2f° par pas à pleine vitesse", step*180/math.Pi)
	}
}
