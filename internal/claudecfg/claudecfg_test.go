package claudecfg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyCommand(t *testing.T) {
	cases := []struct {
		name, settings, want string
	}{
		{"absent", "", ""},
		{"illisible", `{"hooks": `, ""},
		{"sans hooks", `{"theme": "dark"}`, ""},
		{"hook d'un autre", `{"hooks": {"Stop": [{"hooks": [{"type": "command", "command": "mon-script.sh"}]}]}}`, ""},
		{"ancien hook", `{"hooks": {"Stop": [{"hooks": [{"type": "command", "command": "\"E:\\Dev\\peons\\peons.exe\" hook"}]}]}}`, `"E:\Dev\peons\peons.exe" hook`},
		{"avec BOM", "\ufeff" + `{"hooks": {"Stop": [{"hooks": [{"command": "peons hook"}]}]}}`, "peons hook"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if c.settings != "" {
				if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(c.settings), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if got := LegacyCommand(dir); got != c.want {
				t.Errorf("LegacyCommand = %q, attendu %q", got, c.want)
			}
		})
	}
}
