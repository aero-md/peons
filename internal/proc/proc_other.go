//go:build !windows && !linux

package proc

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// FindClaude remonte les parents avec ps (macOS, BSD) jusqu'à un processus
// claude, ou node pour une installation npm. Pas de date de création : started
// vaut toujours 0.
func FindClaude() (pid uint32, started int64, ok bool) {
	var nodeFallback uint32
	pid = uint32(os.Getpid())
	for i := 0; i < 8; i++ {
		ppid, _, found := ps(pid)
		if !found || ppid == 0 {
			break
		}
		pid = ppid
		_, comm, found := ps(pid)
		if !found {
			break
		}
		switch comm[strings.LastIndexByte(comm, '/')+1:] {
		case "claude":
			return pid, 0, true
		case "node":
			if nodeFallback == 0 {
				nodeFallback = pid
			}
		}
	}
	return nodeFallback, 0, nodeFallback != 0
}

// Alive dit si le processus existe encore (started n'est pas utilisé ici).
func Alive(pid uint32, _ int64) bool {
	err := syscall.Kill(int(pid), 0)
	return err == nil || err == syscall.EPERM
}

func ps(pid uint32) (ppid uint32, comm string, ok bool) {
	out, err := exec.Command("ps", "-o", "ppid=,comm=", "-p", strconv.FormatUint(uint64(pid), 10)).Output()
	if err != nil {
		return 0, "", false
	}
	f := strings.Fields(strings.TrimSpace(string(out)))
	if len(f) < 2 {
		return 0, "", false
	}
	p, err := strconv.ParseUint(f[0], 10, 32)
	if err != nil {
		return 0, "", false
	}
	return uint32(p), strings.Join(f[1:], " "), true
}

// Focus : pas de prise en charge hors Windows.
func Focus(pid uint32, title string) bool { return false }
