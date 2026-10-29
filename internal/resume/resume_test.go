package resume

import (
	"testing"
	"time"
)

func TestCycle(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir()) // UserConfigDir sous Windows
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	if err := Start(Entry{ID: "a", Cwd: "/p"}); err != nil {
		t.Fatal(err)
	}
	first, _ := read("a")
	time.Sleep(2 * time.Millisecond)
	if err := Done("a"); err != nil {
		t.Fatal(err)
	}
	if l, _ := List(false); len(l) != 0 {
		t.Fatalf("fiche terminée encore listée : %v", l)
	}
	if err := Start(Entry{ID: "a", Cwd: "/p"}); err != nil { // session reprise
		t.Fatal(err)
	}
	l, _ := List(false)
	if len(l) != 1 || !l[0].Opened.Equal(first.Opened) {
		t.Fatalf("reprise : fiche rouverte attendue avec son Opened d'origine, reçu %v", l)
	}
	for _, bad := range []string{"", "..", "../x", `a\b`, "a/b", "c:x"} {
		if Start(Entry{ID: bad}) == nil {
			t.Errorf("id %q accepté", bad)
		}
	}
}
