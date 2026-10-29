package main

import (
	"fmt"
	"os"
)

// guiErrors : lancé par un double-clic (pas de terminal), une erreur s'affiche dans une boîte de message.
var guiErrors bool

func fail(err error) {
	logf("erreur : %v", err)
	if guiErrors {
		showError(err.Error())
	}
	fmt.Fprintln(os.Stderr, "peons :", err)
	os.Exit(1)
}
