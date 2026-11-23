package main

import "testing"

func TestTipScaleIsIndependentOfClawdSize(t *testing.T) {
	for _, c := range []struct {
		size, display, want float64
	}{
		{1, 1, 1},
		{1, 1.5, 2}, // taille par défaut, écran à 150 % : comme avant
		{1, 2, 2},
		{2, 1, 2},
		{4, 1, 4},
		{2, 1.5, 3},
		{1.5, 1, 1.5},
		{3.5, 1, 3.5},
		{1.5, 1.5, 2.5}, // 2,25 : au demi le plus proche
		{1.5, 2, 3},
		{0, 1, 1}, // jamais sous 1
	} {
		if got := tipScale(c.size, c.display); got != c.want {
			t.Errorf("tipScale(%g, %.2f) = %g, attendu %g", c.size, c.display, got, c.want)
		}
	}
}

func TestClampTipSize(t *testing.T) {
	for in, want := range map[float64]float64{
		-2: minTipSize, 0: defaultTipSize, 1: 1, 1.5: 1.5, 3: 3, 3.5: 3.5, 4: 4, 4.5: maxTipSize, 99: maxTipSize,
		1.3: 1.5, 1.2: 1, 2.74: 2.5, // un prefs.json retouché à la main : calé sur un pas
	} {
		if got := clampTipSize(in); got != want {
			t.Errorf("clampTipSize(%g) = %g, attendu %g", in, got, want)
		}
	}
}

// Le stepper va de 1× à 4× par demi-pas, sans en sauter ni dépasser.
func TestTipSizeSteps(t *testing.T) {
	var got []float64
	for s := minTipSize; ; {
		got = append(got, s)
		next := clampTipSize(s + tipStep)
		if next == s {
			break
		}
		s = next
	}
	want := []float64{1, 1.5, 2, 2.5, 3, 3.5, 4}
	if len(got) != len(want) {
		t.Fatalf("paliers %v, attendu %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("paliers %v, attendu %v", got, want)
		}
	}
	if clampTipSize(minTipSize-tipStep) != minTipSize {
		t.Error("le − sous 1× doit rester à 1×")
	}
}

func TestFmtTipSize(t *testing.T) {
	for in, want := range map[float64]string{1: "1×", 1.5: "1,5×", 2: "2×", 3.5: "3,5×"} {
		if got := fmtTipSize(in); got != want {
			t.Errorf("fmtTipSize(%g) = %q, attendu %q", in, got, want)
		}
	}
}
