// Package resume garde, pour chaque session Claude Code, une fiche sur disque
// écrite par le hook dès le démarrage : si le PC plante ou si le terminal se
// ferme, la fiche dit quoi reprendre (`claude --resume <id>` dans le dossier du
// projet). Une fiche ne se termine que sur ordre explicite (Done) : un SessionEnd
// ou un processus mort ne prouvent pas que le sujet est fini.
//
// Une fiche par fichier : des hooks concurrents n'écrivent jamais le même.
package resume

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Entry est la fiche d'une session.
type Entry struct {
	ID         string    `json:"id"`
	Cwd        string    `json:"cwd"`
	Transcript string    `json:"transcript,omitempty"`
	Opened     time.Time `json:"opened"`
	PID        uint32    `json:"pid,omitempty"`     // processus Claude Code, pour savoir s'il tourne encore
	Started    int64     `json:"started,omitempty"` // sa date de création, contre la réutilisation du PID
	Done       bool      `json:"done,omitempty"`
}

func dir() (string, error) {
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "peons", "sessions"), nil
}

// file refuse un id qui sortirait du dossier : il vient d'un hook.
func file(id string) (string, error) {
	if id == "" || id != filepath.Base(id) || strings.ContainsAny(id, `/\:`) || id == ".." {
		return "", errors.New("identifiant de session invalide")
	}
	d, err := dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, id+".json"), nil
}

func write(e Entry) error {
	f, err := file(e.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	tmp := f + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f)
}

func read(id string) (Entry, error) {
	f, err := file(id)
	if err != nil {
		return Entry{}, err
	}
	data, err := os.ReadFile(f)
	if err != nil {
		return Entry{}, err
	}
	var e Entry
	return e, json.Unmarshal(data, &e)
}

// Start crée la fiche, ou la rouvre si la session est reprise (--resume) : la
// date d'ouverture d'origine est gardée, la fiche redevient ouverte.
func Start(e Entry) error {
	if old, err := read(e.ID); err == nil && !old.Opened.IsZero() {
		e.Opened = old.Opened
	}
	if e.Opened.IsZero() {
		e.Opened = time.Now()
	}
	e.Done = false
	return write(e)
}

// Done termine la fiche d'une session.
func Done(id string) error {
	e, err := read(id)
	if err != nil {
		return err
	}
	e.Done = true
	return write(e)
}

// List donne les fiches, la plus ancienne ouverture d'abord ; les terminées
// seulement si all.
func List(all bool) ([]Entry, error) {
	d, err := dir()
	if err != nil {
		return nil, err
	}
	names, err := os.ReadDir(d)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, n := range names {
		id, ok := strings.CutSuffix(n.Name(), ".json")
		if !ok {
			continue
		}
		if e, err := read(id); err == nil && (all || !e.Done) {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b Entry) int { return a.Opened.Compare(b.Opened) })
	return out, nil
}
