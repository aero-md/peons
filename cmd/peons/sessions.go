package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"peons/internal/proc"
	"peons/internal/resume"
	"peons/internal/transcript"
)

// runSessions liste les fiches de reprise : « en cours » si le processus Claude Code
// tourne encore, sinon « interrompue » — c'est celle-là qu'on veut retrouver.
func runSessions() {
	all := flag.Bool("all", false, "avec les sessions terminées")
	flag.Parse()
	list, err := resume.List(*all)
	if err != nil {
		fail(err)
	}
	if len(list) == 0 {
		fmt.Println("aucune session à reprendre")
		return
	}
	for _, e := range list {
		state := "interrompue"
		switch {
		case e.Done:
			state = "terminée"
		case e.PID != 0 && proc.Alive(e.PID, e.Started):
			state = "en cours"
		}
		title := ""
		if info, ok := transcript.Scan(e.Transcript); ok {
			title = info.Title
		}
		fmt.Printf("%s  %-11s %s  %s\n", e.ID, state, e.Opened.Local().Format("02/01 15:04"), title)
		if state == "interrompue" {
			fmt.Printf("    cd %q && claude --resume %s\n", e.Cwd, e.ID)
		} else {
			fmt.Printf("    %s\n", e.Cwd)
		}
	}
}

// runDone termine une fiche : par identifiant (ou son début), ou, sans argument, la
// seule fiche ouverte du dossier courant.
func runDone() {
	flag.Parse()
	list, err := resume.List(false)
	if err != nil {
		fail(err)
	}
	var match []resume.Entry
	for _, e := range list {
		if arg := flag.Arg(0); arg != "" && strings.HasPrefix(e.ID, arg) || arg == "" && sameDir(e.Cwd) {
			match = append(match, e)
		}
	}
	if len(match) != 1 {
		fail(fmt.Errorf("%d sessions ouvertes correspondent : précisez l'identifiant (peons sessions)", len(match)))
	}
	if err := resume.Done(match[0].ID); err != nil {
		fail(err)
	}
	fmt.Println("terminée :", match[0].ID)
}

func sameDir(cwd string) bool {
	here, err := os.Getwd()
	return err == nil && strings.EqualFold(filepath.Clean(here), filepath.Clean(cwd))
}
