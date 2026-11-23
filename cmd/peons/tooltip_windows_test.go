package main

import (
	"image"
	"testing"
)

// Pour une taille entière, les mesures sont exactement celles d'avant les demi-pas.
func TestTipUnitsUnchangedForWholeSizes(t *testing.T) {
	for k := 1; k <= 4; k++ {
		u := tipUnits{float64(k)}
		if u.thin() != k {
			t.Errorf("%d× : trait de %d px, attendu %d", k, u.thin(), k)
		}
		for _, n := range []int{2, 4, 5, 7, 15} {
			if got := u.px(n); got != n*k {
				t.Errorf("%d× : px(%d) = %d, attendu %d", k, n, got, n*k)
			}
		}
	}
}

// Chaque demi-pas agrandit l'étiquette, en largeur et en hauteur.
func TestTipGrowsWithEachHalfStep(t *testing.T) {
	info := tipInfo{
		Name: "Une session", Sub: "peons", State: "travaille", Since: "3 min 05 s", Tool: "Bash",
		ContextOK: true, Fraction: 0.4, Remaining: 120_000, WindowSize: 200_000,
	}
	var tip layeredTip
	defer tip.Destroy()
	var prev image.Point
	for s := minTipSize; s <= maxTipSize; s += tipStep {
		face, err := tip.loadFace(s)
		if err != nil {
			t.Fatal(err)
		}
		tip.compose(info, face, tipUnits{s})
		size := tip.img.Rect.Size()
		if size.X <= prev.X || size.Y <= prev.Y {
			t.Errorf("%s : %v, pas plus grand que le palier d'avant (%v)", fmtTipSize(s), size, prev)
		}
		prev = size
	}
}
