package main

import (
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

const (
	detachedProcess     = 0x00000008
	createNewProcessGrp = 0x00000200

	attachParentProcess = ^uintptr(0) // (DWORD)-1
	genericWrite        = 0x40000000
	mbOK, mbIconError   = 0x0, 0x10
	mbIconWarning       = 0x30
)

var (
	attachConsole = kernel32.NewProc("AttachConsole")
	messageBoxW   = user32.NewProc("MessageBoxW")
)

// startDetached lance « peons run -background » sans console et indépendamment
// du hook, du terminal et de Claude Code : il leur survit. -background : pas de
// fenêtre de réglages, l'application est là pour suivre les sessions.
func startDetached() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "run", "-background")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewProcessGrp}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// freeConsole est sans objet : l'exécutable est construit pour le sous-système
// graphique (-H=windowsgui), il n'ouvre jamais de console tout seul.
func freeConsole() {}

// useConsole rattache les sorties au terminal qui a lancé l'exécutable (il n'en
// a pas par défaut, étant graphique), pour « -trace ».
// Sans effet si la sortie est déjà redirigée ou s'il n'y a pas de terminal.
func useConsole() {
	if h, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE); err == nil && h != 0 && h != syscall.InvalidHandle {
		return
	}
	if r, _, _ := attachConsole.Call(attachParentProcess); r == 0 {
		return
	}
	name, _ := syscall.UTF16PtrFromString("CONOUT$")
	h, err := syscall.CreateFile(name, genericWrite, syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		return
	}
	os.Stdout = os.NewFile(uintptr(h), "stdout")
	os.Stderr = os.Stdout
}

// showError montre une erreur de démarrage : sans console, l'utilisateur qui a
// double-cliqué sur l'exécutable ne verrait sinon rien.
func showError(text string) { messageBox(text, mbIconError) }

// showNotice avertit l'utilisateur sans bloquer l'application.
func showNotice(text string) { go messageBox(text, mbIconWarning) }

func messageBox(text string, icon uintptr) {
	t, _ := syscall.UTF16PtrFromString(text)
	c, _ := syscall.UTF16PtrFromString("peons")
	messageBoxW.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), mbOK|icon)
}
