package proc

import (
	"errors"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// UI Automation, appelée à la main : x/sys/windows ne fournit pas les interfaces
// COM, on passe donc par les tables de fonctions (vtables) de celles qu'on utilise.
var (
	procCoCreateInstance = windows.NewLazySystemDLL("ole32.dll").NewProc("CoCreateInstance")
	procSysFreeString    = windows.NewLazySystemDLL("oleaut32.dll").NewProc("SysFreeString")

	clsidUIAutomation = windows.GUID{Data1: 0xff48dba4, Data2: 0x60ef, Data3: 0x4201, Data4: [8]byte{0xaa, 0x87, 0x54, 0x10, 0x3e, 0xef, 0x59, 0x4e}}
	iidUIAutomation   = windows.GUID{Data1: 0x30cbe57d, Data2: 0xd9d0, Data3: 0x452a, Data4: [8]byte{0xab, 0x13, 0x7a, 0xc5, 0xac, 0x48, 0x25, 0xee}}
	iidSelectionItem  = windows.GUID{Data1: 0xa8efa66a, Data2: 0x0fda, Data3: 0x421a, Data4: [8]byte{0x91, 0x94, 0x38, 0x02, 0x1f, 0x35, 0x78, 0xea}}
)

const (
	treeScopeDescendants  = 4
	propControlType       = 30003 // UIA_ControlTypePropertyId
	controlTypeTabItem    = 50019 // UIA_TabItemControlTypeId
	patternSelectionItem  = 10010 // UIA_SelectionItemPatternId
	varTypeI4             = 3     // VT_I4
	vtblRelease           = 2
	vtblElementFromHandle = 6  // IUIAutomation
	vtblCreatePropertyCnd = 23 // IUIAutomation
	vtblFindAll           = 6  // IUIAutomationElement
	vtblGetCurrentName    = 23 // IUIAutomationElement
	vtblGetCurrentPattern = 14 // IUIAutomationElement::GetCurrentPatternAs
	vtblArrayLength       = 3  // IUIAutomationElementArray
	vtblArrayElement      = 4  // IUIAutomationElementArray
	vtblSelect            = 3  // IUIAutomationSelectionItemPattern
)

// variant : VARIANT de 16 octets ; seul VT_I4 nous sert. Un VARIANT se passe par
// pointeur dans l'ABI x64 de Windows.
type variant struct {
	vt   uint16
	_    [6]byte
	val  int64
	_pad int64
}

// com appelle la méthode d'index idx de la table de l'objet COM obj.
func com(obj unsafe.Pointer, idx int, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(obj)
	fn := *(*uintptr)(unsafe.Add(vtbl, idx*int(unsafe.Sizeof(uintptr(0)))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{uintptr(obj)}, args...)...)
	return r
}

func release(obj unsafe.Pointer) {
	if obj != nil {
		com(obj, vtblRelease)
	}
}

func failed(hr uintptr) bool { return int32(hr) < 0 }

// selectTabItem sélectionne, parmi les onglets (TabItem) de la fenêtre hwnd, le
// premier dont le nom satisfait match. Faux si aucun ne correspond ou si UI
// Automation échoue.
func selectTabItem(hwnd uintptr, match func(name string) bool) bool {
	runtime.LockOSThread() // l'appartement COM est celui du fil
	defer runtime.UnlockOSThread()
	// S_FALSE (1) : déjà initialisé sur ce fil, il faut quand même rendre la main ensuite
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err == nil || errors.Is(err, syscall.Errno(1)) {
		defer windows.CoUninitialize()
	}

	var uia unsafe.Pointer
	if hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidUIAutomation)), 0, windows.CLSCTX_INPROC_SERVER,
		uintptr(unsafe.Pointer(&iidUIAutomation)), uintptr(unsafe.Pointer(&uia))); failed(hr) || uia == nil {
		return false
	}
	defer release(uia)

	var window unsafe.Pointer
	if failed(com(uia, vtblElementFromHandle, hwnd, uintptr(unsafe.Pointer(&window)))) || window == nil {
		return false
	}
	defer release(window)

	tabs := variant{vt: varTypeI4, val: controlTypeTabItem}
	var cond unsafe.Pointer
	if failed(com(uia, vtblCreatePropertyCnd, propControlType, uintptr(unsafe.Pointer(&tabs)), uintptr(unsafe.Pointer(&cond)))) || cond == nil {
		return false
	}
	defer release(cond)

	var found unsafe.Pointer
	if failed(com(window, vtblFindAll, treeScopeDescendants, uintptr(cond), uintptr(unsafe.Pointer(&found)))) || found == nil {
		return false
	}
	defer release(found)

	var n int32
	com(found, vtblArrayLength, uintptr(unsafe.Pointer(&n)))
	for i := 0; i < int(n); i++ {
		var tab unsafe.Pointer
		if failed(com(found, vtblArrayElement, uintptr(i), uintptr(unsafe.Pointer(&tab)))) || tab == nil {
			continue
		}
		ok := match(elementName(tab)) && selectItem(tab)
		release(tab)
		if ok {
			return true
		}
	}
	return false
}

func elementName(el unsafe.Pointer) string {
	var bstr *uint16
	if failed(com(el, vtblGetCurrentName, uintptr(unsafe.Pointer(&bstr)))) || bstr == nil {
		return ""
	}
	defer procSysFreeString.Call(uintptr(unsafe.Pointer(bstr)))
	return windows.UTF16PtrToString(bstr)
}

func selectItem(el unsafe.Pointer) bool {
	var pattern unsafe.Pointer
	if failed(com(el, vtblGetCurrentPattern, patternSelectionItem, uintptr(unsafe.Pointer(&iidSelectionItem)), uintptr(unsafe.Pointer(&pattern)))) || pattern == nil {
		return false
	}
	defer release(pattern)
	return !failed(com(pattern, vtblSelect))
}
