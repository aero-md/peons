package proc

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// FindClaude remonte les parents jusqu'à un processus nommé claude (ou node pour
// une installation npm) en lisant /proc.
func FindClaude() (pid uint32, started int64, ok bool) {
	var nodeFallback uint32
	pid = uint32(os.Getpid())
	for i := 0; i < 8; i++ {
		_, ppid, _, found := stat(pid)
		if !found || ppid == 0 {
			break
		}
		pid = ppid
		comm, _, start, found := stat(pid)
		if !found {
			break
		}
		switch comm {
		case "claude":
			return pid, start, true
		case "node":
			if nodeFallback == 0 {
				nodeFallback = pid
			}
		}
	}
	if nodeFallback != 0 {
		if _, _, start, found := stat(nodeFallback); found {
			return nodeFallback, start, true
		}
	}
	return 0, 0, false
}

// Alive dit si le processus existe encore ; started (starttime de /proc, en
// ticks depuis le démarrage) écarte un PID réutilisé.
func Alive(pid uint32, started int64) bool {
	if err := syscall.Kill(int(pid), 0); err != nil && err != syscall.EPERM {
		return false
	}
	if started == 0 {
		return true
	}
	_, _, start, found := stat(pid)
	return !found || start == started
}

// stat lit /proc/<pid>/stat : nom, parent et date de démarrage. Le nom est entre
// parenthèses et peut contenir des espaces, d'où la coupe sur la dernière « ) ».
func stat(pid uint32) (comm string, ppid uint32, start int64, ok bool) {
	raw, err := os.ReadFile("/proc/" + strconv.FormatUint(uint64(pid), 10) + "/stat")
	if err != nil {
		return "", 0, 0, false
	}
	s := string(raw)
	l, r := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if l < 0 || r < l {
		return "", 0, 0, false
	}
	fields := strings.Fields(s[r+1:]) // state, ppid, … : starttime est le 20e champ après le nom
	if len(fields) < 20 {
		return "", 0, 0, false
	}
	p, err1 := strconv.ParseUint(fields[1], 10, 32)
	st, err2 := strconv.ParseInt(fields[19], 10, 64)
	if err1 != nil || err2 != nil {
		return "", 0, 0, false
	}
	return s[l+1 : r], uint32(p), st, true
}

// Focus : pas de prise en charge hors Windows.
func Focus(pid uint32, title string) bool { return false }
