package main

import (
	"image"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Tout le dialogue avec Windows passe par ces DLL système. NewLazySystemDLL les
// charge depuis System32 seulement : une DLL du même nom déposée à côté de
// l'exécutable (ou dans le dossier courant) n'est jamais prise.
var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")

	procRegisterClass       = user32.NewProc("RegisterClassExW")
	procCreateWindow        = user32.NewProc("CreateWindowExW")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procDefWindowProc       = user32.NewProc("DefWindowProcW")
	procShowWindow          = user32.NewProc("ShowWindow")
	procSetWindowPos        = user32.NewProc("SetWindowPos")
	procIsIconic            = user32.NewProc("IsIconic")
	procGetClientRect       = user32.NewProc("GetClientRect")
	procAdjustWindowRect    = user32.NewProc("AdjustWindowRectExForDpi")
	procSendMessage         = user32.NewProc("SendMessageW")
	procPostMessage         = user32.NewProc("PostMessageW")
	procPeekMessage         = user32.NewProc("PeekMessageW")
	procDispatchMessage     = user32.NewProc("DispatchMessageW")
	procMsgWait             = user32.NewProc("MsgWaitForMultipleObjectsEx")
	procRegisterWindowMsg   = user32.NewProc("RegisterWindowMessageW")
	procUpdateLayered       = user32.NewProc("UpdateLayeredWindow")
	procGetDC               = user32.NewProc("GetDC")
	procReleaseDC           = user32.NewProc("ReleaseDC")
	procBeginPaint          = user32.NewProc("BeginPaint")
	procEndPaint            = user32.NewProc("EndPaint")
	procSetCapture          = user32.NewProc("SetCapture")
	procReleaseCapture      = user32.NewProc("ReleaseCapture")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procTrackMouseEvent     = user32.NewProc("TrackMouseEvent")
	procLoadCursor          = user32.NewProc("LoadCursorW")
	procGetDpiForWindow     = user32.NewProc("GetDpiForWindow")
	procSetDpiAwareness     = user32.NewProc("SetProcessDpiAwarenessContext")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procSetWinEventHook     = user32.NewProc("SetWinEventHook")
	procUnhookWinEvent      = user32.NewProc("UnhookWinEvent")
	procGetWindowLongPtr    = user32.NewProc("GetWindowLongPtrW")
	procMonitorFromPoint    = user32.NewProc("MonitorFromPoint")
	procMonitorFromRect     = user32.NewProc("MonitorFromRect")
	procGetMonitorInfo      = user32.NewProc("GetMonitorInfoW")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenu          = user32.NewProc("AppendMenuW")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procCreateIconIndirect  = user32.NewProc("CreateIconIndirect")
	procDestroyIcon         = user32.NewProc("DestroyIcon")

	procGetModuleHandle  = kernel32.NewProc("GetModuleHandleW")
	procCreateCompatDC   = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection = gdi32.NewProc("CreateDIBSection")
	procCreateBitmap     = gdi32.NewProc("CreateBitmap")
	procSelectObject     = gdi32.NewProc("SelectObject")
	procDeleteObject     = gdi32.NewProc("DeleteObject")
	procDeleteDC         = gdi32.NewProc("DeleteDC")
	procBitBlt           = gdi32.NewProc("BitBlt")
	procShellNotifyIcon  = shell32.NewProc("Shell_NotifyIconW")
)

const (
	wmNull       = 0x0000
	wmDestroy    = 0x0002
	wmSize       = 0x0005
	wmPaint      = 0x000F
	wmClose      = 0x0010
	wmQueryEnd   = 0x0011
	wmQuit       = 0x0012
	wmEndSession = 0x0016
	wmSetIcon    = 0x0080

	wmApp  = 0x8000
	wmWake = wmApp + 1 // réveille la boucle depuis une autre goroutine
	wmTray = wmApp + 2 // message de rappel de l'icône de la zone de notification

	qsAllInput         = 0x04FF
	mwmoInputAvailable = 0x0004
	pmRemove           = 0x0001

	monitorNull    = 0
	monitorPrimary = 1
	monitorNearest = 2

	dpiPerMonitorV2 = ^uintptr(3) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 (-4)
)

// winPoint : POINT.
type winPoint struct{ x, y int32 }

// winRect : RECT.
type winRect struct{ left, top, right, bottom int32 }

func (r winRect) rect() image.Rectangle {
	return image.Rect(int(r.left), int(r.top), int(r.right), int(r.bottom))
}

// winMsg : MSG.
type winMsg struct {
	hwnd           uintptr
	message        uint32
	wparam, lparam uintptr
	time           uint32
	pt             winPoint
}

// monitorInfo : MONITORINFO.
type monitorInfo struct {
	size          uint32
	monitor, work winRect
	flags         uint32
}

var (
	// uiWnd : la fenêtre cachée qui reçoit les réveils et l'icône de la zone de
	// notification. Zéro tant que l'interface n'est pas montée (les tests).
	uiWnd       atomic.Uintptr
	wakePending atomic.Bool
	// uiApp : l'application, pour les procédures de fenêtre (qui n'ont que leur HWND).
	uiApp *app

	mainProc = windows.NewCallback(mainWndProc)
	// msgTaskbarCreated : l'Explorateur a redémarré, l'icône est à remettre.
	msgTaskbarCreated uint32
)

// setDPIAware rend le processus sensible à la résolution de chaque écran : toutes
// les coordonnées sont alors des pixels physiques. À faire avant la première fenêtre.
func setDPIAware() { procSetDpiAwareness.Call(dpiPerMonitorV2) }

// createMainWindow crée la fenêtre cachée (jamais montrée) de l'application.
func createMainWindow() error {
	inst, _, _ := procGetModuleHandle.Call(0)
	name, _ := windows.UTF16PtrFromString("peonsMain")
	class := wndClassEx{wndProc: mainProc, instance: inst, className: name}
	class.size = uint32(unsafe.Sizeof(class))
	if r, _, err := procRegisterClass.Call(uintptr(unsafe.Pointer(&class))); r == 0 {
		return &winError{"RegisterClassEx", err}
	}
	hwnd, _, err := procCreateWindow.Call(0, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(name)), wsPopup, 0, 0, 0, 0, 0, 0, inst, 0)
	if hwnd == 0 {
		return &winError{"CreateWindowEx", err}
	}
	tb, _ := windows.UTF16PtrFromString("TaskbarCreated")
	r, _, _ := procRegisterWindowMsg.Call(uintptr(unsafe.Pointer(tb)))
	msgTaskbarCreated = uint32(r)
	uiWnd.Store(hwnd)
	return nil
}

func destroyMainWindow() {
	if h := uiWnd.Swap(0); h != 0 {
		procDestroyWindow.Call(h)
	}
}

type winError struct {
	call string
	err  error
}

func (e *winError) Error() string { return e.call + " : " + e.err.Error() }
func (e *winError) Unwrap() error { return e.err }

func mainWndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	switch {
	case msg == wmWake:
		wakePending.Store(false)
		return 0
	case msg == wmTray:
		if a := uiApp; a != nil && a.tray != nil {
			a.tray.onMessage(lparam)
		}
		return 0
	case msg == uintptr(msgTaskbarCreated) && msgTaskbarCreated != 0:
		if a := uiApp; a != nil && a.tray != nil {
			a.tray.add()
		}
		return 0
	case msg == wmQueryEnd:
		return 1 // la session peut se fermer
	case msg == wmEndSession:
		if wparam != 0 {
			quit.Store(true)
		}
		return 0
	}
	r, _, _ := procDefWindowProc.Call(hwnd, msg, wparam, lparam)
	return r
}

// wake réveille la boucle d'événements depuis n'importe quelle goroutine. Les
// réveils qui se suivent avant qu'elle ne s'exécute n'en font qu'un.
func wake() {
	if h := uiWnd.Load(); h != 0 && wakePending.CompareAndSwap(false, true) {
		procPostMessage.Call(h, wmWake, 0, 0)
	}
}

// waitMessages dort jusqu'à un message ou jusqu'à d écoulé (d négatif : sans
// limite), puis distribue tous les messages en attente. Vrai : WM_QUIT.
func waitMessages(d time.Duration) (quit bool) {
	ms := uintptr(windows.INFINITE)
	if d >= 0 {
		ms = uintptr(max(1, (d+time.Millisecond-1)/time.Millisecond))
	}
	procMsgWait.Call(0, 0, ms, qsAllInput, mwmoInputAvailable)
	var m winMsg
	for {
		if r, _, _ := procPeekMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, pmRemove); r == 0 {
			return false
		}
		if m.message == wmQuit {
			return true
		}
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// monitorBounds donne l'écran entier et sa zone utilisable (sans la barre des
// tâches) d'un HMONITOR.
func monitorBounds(mon uintptr) (bounds, work image.Rectangle, ok bool) {
	if mon == 0 {
		return image.Rectangle{}, image.Rectangle{}, false
	}
	mi := monitorInfo{}
	mi.size = uint32(unsafe.Sizeof(mi))
	if r, _, _ := procGetMonitorInfo.Call(mon, uintptr(unsafe.Pointer(&mi))); r == 0 {
		return image.Rectangle{}, image.Rectangle{}, false
	}
	return mi.monitor.rect(), mi.work.rect(), true
}

// monitorFromPoint : l'écran qui porte p. flags : monitorNull (aucun si hors de tout
// écran), monitorPrimary ou monitorNearest.
func monitorFromPoint(p image.Point, flags uintptr) uintptr {
	packed := uintptr(uint32(int32(p.X))) | uintptr(uint32(int32(p.Y)))<<32 // POINT passé par valeur
	mon, _, _ := procMonitorFromPoint.Call(packed, flags)
	return mon
}

// monitorFromRect : l'écran qui porte la plus grande part de r (ou le plus proche).
func monitorFromRect(r image.Rectangle, flags uintptr) uintptr {
	rc := winRect{int32(r.Min.X), int32(r.Min.Y), int32(r.Max.X), int32(r.Max.Y)}
	mon, _, _ := procMonitorFromRect.Call(uintptr(unsafe.Pointer(&rc)), flags)
	return mon
}

// iconFromImage fabrique une icône Windows (alpha par pixel) à partir d'une image.
// L'appelant la détruit avec DestroyIcon.
func iconFromImage(img *image.NRGBA) uintptr {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	var pixels dibSection
	pixels.create(w, h)
	defer pixels.free()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			s := img.Pix[img.PixOffset(b.Min.X+x, b.Min.Y+y):]
			d := pixels.bits[(y*w+x)*4:]
			d[0], d[1], d[2], d[3] = s[2], s[1], s[0], s[3] // l'alpha des icônes n'est pas prémultiplié
		}
	}
	// le bitmap de couleur ne doit pas être sélectionné dans un contexte pour
	// CreateIconIndirect ; sa mémoire reste valide jusqu'à free
	procDeleteDC.Call(pixels.memDC)
	pixels.memDC = 0
	mask := make([]byte, (w+15)/16*2*h) // masque monochrome, à zéro : l'alpha décide
	hmask, _, _ := procCreateBitmap.Call(uintptr(w), uintptr(h), 1, 1, uintptr(unsafe.Pointer(&mask[0])))
	if hmask == 0 {
		return 0
	}
	defer procDeleteObject.Call(hmask)
	info := struct {
		icon               int32
		xHotspot, yHotspot uint32
		mask, color        uintptr
	}{icon: 1, mask: hmask, color: pixels.bitmap}
	icon, _, _ := procCreateIconIndirect.Call(uintptr(unsafe.Pointer(&info)))
	return icon
}
