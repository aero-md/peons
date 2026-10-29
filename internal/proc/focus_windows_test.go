package proc

import "testing"

func TestSameTitle(t *testing.T) {
	for _, c := range []struct {
		tab, session string
		want         bool
	}{
		{"✳ peons", "peons", true},
		{"◐ peons", "Peons", true},
		{"✳ rag confidentialit", "rag confidentialité", true}, // titre d'onglet tronqué
		{"✳ mcp next", "peons", false},
		{"", "peons", false},
		{"✳ peons", "", false},
	} {
		if got := SameTitle(c.tab, c.session); got != c.want {
			t.Errorf("SameTitle(%q, %q) = %v, attendu %v", c.tab, c.session, got, c.want)
		}
	}
}
