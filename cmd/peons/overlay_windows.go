package main

import (
	"fmt"
	"image"
	"unsafe"

	"golang.org/x/sys/windows"
)

// layeredOverlay : fenêtre Windows superposée (WS_EX_LAYERED) dont l'image est
// fournie par UpdateLayeredWindow avec un alpha par pixel. Windows s'en sert aussi
// pour le clic : un pixel entièrement transparent laisse passer la souris vers la
// fenêtre dessous. Pas de forme à calculer, pas de composition DWM : plus de
// clignotement au changement de forme ni de teinte plaquée au changement de focus,
// et pas de GPU (l'image est composée ici, en mémoire).
//
// Les messages de la fenêtre sont distribués par la pompe de messages de la boucle
// principale (waitMessages), sur ce même fil : la procédure de fenêtre ne fait que
// noter les entrées (pushPointer), la boucle les traite à son passage.
type layeredOverlay struct {
	hwnd     uintptr
	dib      dibSection // image affichée : BGRA, alpha prémultiplié, w×h
	island   []byte     // l'île dans le même format
	w, h     int
	x, y     int
	tracking bool    // Windows a été prié de signaler la sortie de la souris
	hook     uintptr // veille du premier plan, voir keepOnTop
	visible  bool
}

// dibSection est une image 32 bits (BGRA, alpha prémultiplié) que Windows lit pour
// UpdateLayeredWindow ; bits en est la mémoire, valable jusqu'à free.
type dibSection struct {
	memDC, bitmap uintptr
	bits          []byte
	w, h          int
}

// create alloue l'image (lignes de haut en bas), en libérant la précédente.
func (d *dibSection) create(w, h int) {
	d.free()
	screen, _, _ := procGetDC.Call(0)
	d.memDC, _, _ = procCreateCompatDC.Call(screen)
	procReleaseDC.Call(0, screen)
	info := bitmapInfoHeader{width: int32(w), height: -int32(h), planes: 1, bpp: 32}
	info.size = uint32(unsafe.Sizeof(info))
	var pixels unsafe.Pointer
	d.bitmap, _, _ = procCreateDIBSection.Call(d.memDC, uintptr(unsafe.Pointer(&info)), 0, uintptr(unsafe.Pointer(&pixels)), 0, 0)
	if d.bitmap == 0 || pixels == nil {
		panic("CreateDIBSection a échoué")
	}
	procSelectObject.Call(d.memDC, d.bitmap)
	d.bits = unsafe.Slice((*byte)(pixels), w*h*4)
	d.w, d.h = w, h
}

// free libère l'image ; bits n'est plus valable ensuite.
func (d *dibSection) free() {
	d.bits = nil
	d.w, d.h = 0, 0
	if d.bitmap != 0 {
		procDeleteObject.Call(d.bitmap)
		d.bitmap = 0
	}
	if d.memDC != 0 {
		procDeleteDC.Call(d.memDC)
		d.memDC = 0
	}
}

var (
	// la procédure de fenêtre : un seul rappel Go pour toute la vie du processus
	overlayProc    = windows.NewCallback(overlayWndProc)
	foregroundProc = windows.NewCallback(onForegroundChange)
	// il n'y a qu'un overlay : la procédure de fenêtre le retrouve ici
	layered *layeredOverlay
)

const (
	csDblClks = 0x0008

	wsExLayered    = 0x00080000
	wsExTopmost    = 0x00000008
	wsExToolWindow = 0x00000080
	wsExNoActivate = 0x08000000
	wsPopup        = 0x80000000

	wmEraseBkgnd    = 0x0014
	wmMouseActivate = 0x0021
	wmMouseMove     = 0x0200
	wmLButtonDown   = 0x0201
	wmLButtonUp     = 0x0202
	wmLButtonDblClk = 0x0203
	wmRButtonDown   = 0x0204
	wmMButtonDown   = 0x0207
	wmMouseLeave    = 0x02A3
	mkLButton       = 0x0001
	maNoActivate    = 3

	swHide           = 0
	swShowNoActivate = 4

	swpNoSize     = 0x0001
	swpNoMove     = 0x0002
	swpNoZOrder   = 0x0004
	swpNoActivate = 0x0010
	hwndTopmost   = ^uintptr(0) // (HWND)-1

	gwlExStyle            = ^uintptr(19) // GWL_EXSTYLE (-20)
	eventSystemForeground = 0x0003
	winEventSkipOwn       = 0x0002 // WINEVENT_SKIPOWNPROCESS (hors contexte : rappel sur notre fil)

	ulwAlpha = 0x00000002
	idcArrow = 32512
)

type wndClassEx struct {
	size, style      uint32
	wndProc          uintptr
	clsExtra, wndExt int32
	instance, icon   uintptr
	cursor, bg       uintptr
	menuName         *uint16
	className        *uint16
	iconSm           uintptr
}

// trackMouseEvent : TRACKMOUSEEVENT.
type trackMouseEvent struct {
	size, flags uint32
	hwnd        uintptr
	hoverTime   uint32
}

const tmeLeave = 0x00000002

type winSize struct{ cx, cy int32 }

// blendFunction : AC_SRC_OVER, alpha de source constant 255, alpha par pixel (AC_SRC_ALPHA).
type blendFunction struct{ op, flags, alpha, format byte }

type bitmapInfoHeader struct {
	size          uint32
	width, height int32
	planes, bpp   uint16
	compression   uint32
	imageSize     uint32
	xppm, yppm    int32
	used, imp     uint32
}

// newOverlay : la fenêtre superposée native.
func newOverlay(*app) (overlay, error) { return newLayeredOverlay() }

func newLayeredOverlay() (*layeredOverlay, error) {
	inst, _, _ := procGetModuleHandle.Call(0)
	cursor, _, _ := procLoadCursor.Call(0, idcArrow)
	name, _ := windows.UTF16PtrFromString("peonsOverlay")
	class := wndClassEx{style: csDblClks, wndProc: overlayProc, instance: inst, cursor: cursor, className: name}
	class.size = uint32(unsafe.Sizeof(class))
	if r, _, err := procRegisterClass.Call(uintptr(unsafe.Pointer(&class))); r == 0 {
		return nil, fmt.Errorf("RegisterClassEx : %w", err)
	}
	hwnd, _, err := procCreateWindow.Call(wsExLayered|wsExTopmost|wsExToolWindow|wsExNoActivate,
		uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(name)), wsPopup, 0, 0, 1, 1, 0, 0, inst, 0)
	if hwnd == 0 {
		return nil, fmt.Errorf("CreateWindowEx : %w", err)
	}
	o := &layeredOverlay{hwnd: hwnd}
	layered = o
	o.hook, _, _ = procSetWinEventHook.Call(eventSystemForeground, eventSystemForeground, 0, foregroundProc, 0, 0, winEventSkipOwn)
	return o, nil
}

// onForegroundChange : une autre fenêtre vient de passer au premier plan.
func onForegroundChange(hook, event, hwnd, idObject, idChild, thread, ms uintptr) uintptr {
	if o := layered; o != nil {
		o.keepOnTop()
	}
	return 0
}

// keepOnTop remet l'overlay en « toujours au-dessus » si Windows lui a retiré
// l'attribut (le z-order n'est posé qu'au Show : rien d'autre ne le garde). Ne touche
// à rien tant que l'attribut est là, pour ne pas se battre avec les autres fenêtres
// topmost.
func (o *layeredOverlay) keepOnTop() {
	if o.hwnd == 0 || !o.visible {
		return
	}
	if style, _, _ := procGetWindowLongPtr.Call(o.hwnd, gwlExStyle); style&wsExTopmost != 0 {
		return
	}
	logf("overlay : attribut topmost perdu, rétabli")
	procSetWindowPos.Call(o.hwnd, hwndTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
}

// overlayWndProc : la souris est notée pour la boucle principale ; le reste est
// laissé à Windows. L'overlay ne prend jamais le focus.
func overlayWndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	switch msg {
	case wmMouseActivate:
		return maNoActivate
	case wmEraseBkgnd:
		return 1
	case wmMouseLeave:
		if o := layered; o != nil {
			o.tracking = false
		}
		pushPointer(pointer{kind: pointerLeave})
		return 0
	case wmLButtonDown, wmLButtonDblClk, wmLButtonUp, wmRButtonDown, wmMButtonDown, wmMouseMove:
		p := pointer{x: float32(int16(lparam)), y: float32(int16(lparam >> 16)), clicks: 1}
		var pt winPoint
		procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
		p.gx, p.gy = float32(pt.x), float32(pt.y)
		switch msg {
		case wmLButtonDown, wmLButtonDblClk:
			procSetCapture.Call(hwnd) // le glisser continue hors de la fenêtre
			p.kind = pointerDown
			if msg == wmLButtonDblClk {
				p.clicks = 2
			}
		case wmRButtonDown:
			p.kind, p.right = pointerDown, true
		case wmMButtonDown:
			p.kind, p.middle = pointerDown, true
		case wmLButtonUp:
			procReleaseCapture.Call()
			p.kind = pointerUp
		default: // la souris bouge : glisser si le bouton gauche est enfoncé, sinon survol
			p.kind = pointerHover
			if wparam&mkLButton != 0 {
				p.kind = pointerMove
			} else if o := layered; o != nil && !o.tracking {
				// Windows ne signale la sortie de la souris que sur demande
				o.tracking = true
				tme := trackMouseEvent{flags: tmeLeave, hwnd: hwnd}
				tme.size = uint32(unsafe.Sizeof(tme))
				procTrackMouseEvent.Call(uintptr(unsafe.Pointer(&tme)))
			}
		}
		pushPointer(p)
		return 0
	}
	r, _, _ := procDefWindowProc.Call(hwnd, msg, wparam, lparam)
	return r
}

func (o *layeredOverlay) Show() {
	o.visible = true
	procShowWindow.Call(o.hwnd, swShowNoActivate)
	procSetWindowPos.Call(o.hwnd, hwndTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
}

func (o *layeredOverlay) Hide() {
	o.visible = false
	procShowWindow.Call(o.hwnd, swHide)
}

func (o *layeredOverlay) Position() (int, int) { return o.x, o.y }

func (o *layeredOverlay) SetPosition(x, y int) {
	o.x, o.y = x, y
	procSetWindowPos.Call(o.hwnd, 0, uintptr(int32(x)), uintptr(int32(y)), 0, 0, swpNoSize|swpNoZOrder|swpNoActivate)
}

func (o *layeredOverlay) DisplayScale() float64 {
	if dpi, _, _ := procGetDpiForWindow.Call(o.hwnd); dpi != 0 {
		return float64(dpi) / 96
	}
	return 1
}

// Resize crée l'image de la fenêtre (une section DIB 32 bits, lue par Windows) et
// y prépare l'îlot.
func (o *layeredOverlay) Resize(w, h int, island *image.NRGBA) {
	o.w, o.h = w, h
	o.dib.create(w, h)
	o.island = make([]byte, w*h*4)
	for i := 0; i+3 < len(island.Pix) && i+3 < len(o.island); i += 4 {
		premultiply(o.island[i:i+4], island.Pix[i], island.Pix[i+1], island.Pix[i+2], island.Pix[i+3])
	}
}
func (o *layeredOverlay) Destroy() {
	layered = nil
	if o.hook != 0 {
		procUnhookWinEvent.Call(o.hook)
	}
	procDestroyWindow.Call(o.hwnd)
	o.dib.free()
}

// premultiply écrit un pixel RGBA en BGRA à alpha prémultiplié, ce qu'attend UpdateLayeredWindow.
func premultiply(dst []byte, r, g, b, a uint8) {
	if a == 255 {
		dst[0], dst[1], dst[2], dst[3] = b, g, r, 255
		return
	}
	dst[0], dst[1], dst[2], dst[3] = uint8(uint16(b)*uint16(a)/255), uint8(uint16(g)*uint16(a)/255), uint8(uint16(r)*uint16(a)/255), a
}

// Draw compose l'îlot et les bonhommes dans l'image de la fenêtre puis la présente.
func (o *layeredOverlay) Draw(a *app) {
	copy(o.dib.bits, o.island)
	for i, v := range a.views {
		for _, p := range a.layout(i, v) {
			o.blit(p.img, p.at, a.ui)
		}
	}
	pt, sz, src := winPoint{int32(o.x), int32(o.y)}, winSize{int32(o.w), int32(o.h)}, winPoint{}
	blend := blendFunction{op: 0, alpha: 255, format: 1}
	procUpdateLayered.Call(o.hwnd, 0, uintptr(unsafe.Pointer(&pt)), uintptr(unsafe.Pointer(&sz)), o.dib.memDC,
		uintptr(unsafe.Pointer(&src)), 0, uintptr(unsafe.Pointer(&blend)), ulwAlpha)
}

// blit pose une image agrandie u fois (au plus proche : des pixels carrés) à la
// place at de la fenêtre, par-dessus ce qui s'y trouve.
func (o *layeredOverlay) blit(img *image.NRGBA, at image.Point, u int) {
	b := img.Bounds()
	var px [4]byte
	for sy := 0; sy < b.Dy(); sy++ {
		for sx := 0; sx < b.Dx(); sx++ {
			i := sy*img.Stride + 4*sx
			alpha := img.Pix[i+3]
			if alpha == 0 {
				continue
			}
			premultiply(px[:], img.Pix[i], img.Pix[i+1], img.Pix[i+2], alpha)
			for yy := 0; yy < u; yy++ {
				dy := at.Y + sy*u + yy
				if dy < 0 || dy >= o.h {
					continue
				}
				for xx := 0; xx < u; xx++ {
					dx := at.X + sx*u + xx
					if dx < 0 || dx >= o.w {
						continue
					}
					d := o.dib.bits[(dy*o.w+dx)*4 : (dy*o.w+dx)*4+4]
					if alpha == 255 {
						copy(d, px[:])
						continue
					}
					inv := 255 - uint16(alpha) // source par-dessus : s + d·(1 − αs)
					for k := 0; k < 4; k++ {
						d[k] = uint8(uint16(px[k]) + uint16(d[k])*inv/255)
					}
				}
			}
		}
	}
}
