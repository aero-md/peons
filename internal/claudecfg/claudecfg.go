// Package claudecfg repère dans les réglages de Claude Code (settings.json) les
// hooks d'une ancienne installation de peons. Les hooks viennent désormais du
// plugin ; ce paquet ne sert qu'à prévenir l'utilisateur, il n'écrit jamais.
package claudecfg

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ourCommand reconnaît nos anciennes commandes : `"…\peons.exe" hook`.
var ourCommand = regexp.MustCompile(`peons(\.exe)?"?\s+hook\s*$`)

// Dir est le dossier de configuration de Claude Code.
func Dir() (string, error) {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

// LegacyCommand donne la commande d'un ancien hook de peons trouvé dans
// settings.json, ou "" s'il n'y en a pas (ou si le fichier est illisible).
func LegacyCommand(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		return ""
	}
	// un settings.json enregistré par PowerShell commence par un BOM
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	var settings struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if json.Unmarshal(data, &settings) != nil {
		return ""
	}
	for _, entries := range settings.Hooks {
		for _, e := range entries {
			for _, h := range e.Hooks {
				if cmd := strings.TrimSpace(h.Command); ourCommand.MatchString(cmd) {
					return cmd
				}
			}
		}
	}
	return ""
}
