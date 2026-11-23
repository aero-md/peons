// Package prefs garde les réglages de l'utilisateur : position de la fenêtre,
// durées du cycle, affichage sans session. Un petit fichier JSON dans le
// dossier de configuration.
package prefs

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Prefs : la fenêtre est repérée par le centre de son bord haut, pour qu'elle
// reste en place quand le nombre de bonhommes la fait grandir ou rétrécir.
// Une durée à zéro veut dire « valeur par défaut ».
type Prefs struct {
	CenterX    int  `json:"center_x"`
	Top        int  `json:"top"`
	Placed     bool `json:"placed"` // CenterX et Top ont été choisis par l'utilisateur
	HappyMin   int  `json:"happy_min,omitempty"`
	NeutralMin int  `json:"neutral_min,omitempty"`
	SadMin     int  `json:"sad_min,omitempty"`
	Scale      int  `json:"scale,omitempty"` // pixels d'écran par pixel de sprite ; 0 : défaut
	// TipSize : taille de l'étiquette de survol, par demi-pas (1, 1,5, 2…) ; 0 : défaut.
	// TipScale est l'ancienne clé, entière : lue tant que TipSize est à zéro, effacée
	// au premier réglage (une nouvelle clé, pour qu'un ancien exe lise encore le fichier).
	TipSize    float64 `json:"tip_size,omitempty"`
	TipScale   int     `json:"tip_scale,omitempty"`
	AlwaysShow bool    `json:"always_show,omitempty"`
	// Taille de l'île : automatique (selon le nombre de bonhommes) tant que
	// IslandManual est faux ; sinon IslandSections sections centrales.
	IslandManual   bool `json:"island_manual,omitempty"`
	IslandSections int  `json:"island_sections,omitempty"`
	// HideIsland : pas d'île, la fenêtre se réduit aux bonhommes.
	HideIsland bool `json:"hide_island,omitempty"`
}

func path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "peons", "prefs.json"), nil
}

// Load lit les préférences ; ok est faux s'il n'y en a pas (ou si le fichier est illisible).
func Load() (p Prefs, ok bool) {
	file, err := path()
	if err != nil {
		return p, false
	}
	data, err := os.ReadFile(file)
	if err != nil || json.Unmarshal(data, &p) != nil {
		return Prefs{}, false
	}
	return p, true
}

// Save écrit les préférences par fichier temporaire + renommage.
func Save(p Prefs) error {
	file, err := path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}
