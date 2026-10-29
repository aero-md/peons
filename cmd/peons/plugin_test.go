package main

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// plugin/hooks/hooks.json est la source des événements écoutés : chacun doit
// appeler « peons.exe hook », et ceux où le hook cherche le processus Claude Code
// (capturesProcess) doivent y figurer.
func TestPluginHooks(t *testing.T) {
	data, err := os.ReadFile("../../plugin/hooks/hooks.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Args []string `json:"args"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	for name, entries := range file.Hooks {
		calls := false
		for _, e := range entries {
			for _, h := range e.Hooks {
				calls = calls || slices.Contains(h.Args, "hook")
			}
		}
		if !calls {
			t.Errorf("%s : aucun hook n'appelle « peons.exe hook »", name)
		}
	}
	for name := range capturesProcess {
		if _, ok := file.Hooks[name]; !ok {
			t.Errorf("%s : absent de hooks.json", name)
		}
	}
}
