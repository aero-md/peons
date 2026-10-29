// exe-icon fabrique l'icône de peons.exe (celle de l'Explorateur, de la barre des
// tâches, d'Alt+Tab) à partir du clawd : même image que l'icône de la zone de
// notification (corps normal, yeux neutres). Il écrit un fichier objet COFF
// « .syso » que « go build » lie tout seul à l'exécutable quand il est dans
// cmd/peons sous le nom rsrc_windows_amd64.syso : pas d'outil tiers, pas de
// compilateur de ressources. Il écrit aussi un .ico si on le demande.
//
//	go run ./tools/exe-icon                      # écrit cmd/peons/rsrc_windows_amd64.syso
//	go run ./tools/exe-icon -ico art/peons.ico   # et le .ico
//
// À relancer quand le sprite du clawd change, puis à commiter le .syso.
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"

	"peons/assets"
	"peons/internal/avatar"
)

// les tailles de l'icône : l'Explorateur choisit la plus proche de ce qu'il affiche
var sizes = []int{16, 32, 48, 64, 128, 256}

func main() {
	out := flag.String("out", "cmd/peons/rsrc_windows_amd64.syso", "fichier objet à écrire")
	ico := flag.String("ico", "", "écrit aussi le .ico (facultatif)")
	flag.Parse()
	if err := run(*out, *ico); err != nil {
		fmt.Fprintln(os.Stderr, "exe-icon :", err)
		os.Exit(1)
	}
}

func run(out, icoPath string) error {
	c, err := avatar.Load(assets.FS, "characters/clawd")
	if err != nil {
		return err
	}
	sprite, err := c.Compose("normal", 0, "neutral")
	if err != nil {
		return err
	}
	sq := square(sprite)
	var pngs [][]byte
	for _, s := range sizes {
		var buf bytes.Buffer
		if err := png.Encode(&buf, render(sq, s)); err != nil {
			return err
		}
		pngs = append(pngs, buf.Bytes())
	}
	if icoPath != "" {
		if err := os.WriteFile(icoPath, ico(pngs), 0o644); err != nil {
			return err
		}
	}
	return os.WriteFile(out, syso(pngs), 0o644)
}

// square rogne l'image à ses pixels visibles, la met dans un carré avec 1 px de marge.
func square(img *image.NRGBA) *image.NRGBA {
	b := img.Bounds()
	r := image.Rectangle{Min: b.Max, Max: b.Min}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.NRGBAAt(x, y).A != 0 {
				r = r.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	if r.Empty() {
		r = b
	}
	side := max(r.Dx(), r.Dy()) + 2
	dst := image.NewNRGBA(image.Rect(0, 0, side, side))
	at := image.Pt((side-r.Dx())/2, (side-r.Dy())/2)
	draw.Draw(dst, image.Rectangle{Min: at, Max: at.Add(r.Size())}, img, r.Min, draw.Src)
	return dst
}

// render donne l'image à la taille s×s : agrandie d'un facteur entier (pixels
// carrés nets, centrée) ou, si s est plus petit que le sprite, réduite par moyenne.
func render(sq *image.NRGBA, s int) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, s, s))
	side := sq.Bounds().Dx()
	if s < side {
		for y := 0; y < s; y++ {
			for x := 0; x < s; x++ {
				x0, x1 := x*side/s, max((x+1)*side/s, x*side/s+1)
				y0, y1 := y*side/s, max((y+1)*side/s, y*side/s+1)
				var r, g, b, a, n int
				for yy := y0; yy < y1; yy++ {
					for xx := x0; xx < x1; xx++ {
						p := sq.NRGBAAt(xx, yy)
						r, g, b, a, n = r+int(p.R)*int(p.A), g+int(p.G)*int(p.A), b+int(p.B)*int(p.A), a+int(p.A), n+1
					}
				}
				if a > 0 { // moyenne pondérée par l'alpha
					dst.Pix[dst.PixOffset(x, y)+0] = uint8(r / a)
					dst.Pix[dst.PixOffset(x, y)+1] = uint8(g / a)
					dst.Pix[dst.PixOffset(x, y)+2] = uint8(b / a)
					dst.Pix[dst.PixOffset(x, y)+3] = uint8(a / n)
				}
			}
		}
		return dst
	}
	k := s / side
	off := (s - side*k) / 2
	for y := 0; y < side*k; y++ {
		for x := 0; x < side*k; x++ {
			dst.SetNRGBA(off+x, off+y, sq.NRGBAAt(x/k, y/k))
		}
	}
	return dst
}

// dim : la largeur ou hauteur d'une entrée d'icône (0 veut dire 256).
func dim(img []byte) byte {
	w := int(binary.BigEndian.Uint32(img[16:20])) // IHDR : largeur d'un PNG
	if w >= 256 {
		return 0
	}
	return byte(w)
}

// ico : le format .ico, des PNG à la suite.
func ico(pngs [][]byte) []byte {
	var b bytes.Buffer
	le := binary.LittleEndian
	binary.Write(&b, le, [3]uint16{0, 1, uint16(len(pngs))})
	off := 6 + 16*len(pngs)
	for _, p := range pngs {
		d := dim(p)
		b.Write([]byte{d, d, 0, 0})
		binary.Write(&b, le, [2]uint16{1, 32})
		binary.Write(&b, le, [2]uint32{uint32(len(p)), uint32(off)})
		off += len(p)
	}
	for _, p := range pngs {
		b.Write(p)
	}
	return b.Bytes()
}

const (
	rtIcon      = 3
	rtGroupIcon = 14
	lang        = 0x0409
)

// syso : un fichier objet COFF amd64 d'une seule section « .rsrc » qui porte les
// ressources : les images (RT_ICON 1..n) et le groupe qui les décrit (RT_GROUP_ICON 1).
// Le lieur Go relie la section telle quelle ; chaque entrée de données donne son
// adresse par une relocalisation (IMAGE_REL_AMD64_ADDR32NB) vers le symbole .rsrc.
func syso(pngs [][]byte) []byte {
	le := binary.LittleEndian
	n := len(pngs)

	// le groupe d'icônes : GRPICONDIR puis une GRPICONDIRENTRY par image
	var grp bytes.Buffer
	binary.Write(&grp, le, [3]uint16{0, 1, uint16(n)})
	for i, p := range pngs {
		d := dim(p)
		grp.Write([]byte{d, d, 0, 0})
		binary.Write(&grp, le, [2]uint16{1, 32})
		binary.Write(&grp, le, uint32(len(p)))
		binary.Write(&grp, le, uint16(i+1))
	}

	// disposition de la section : répertoire racine, répertoires des types, des noms
	// puis des langues, entrées de données, et enfin les données
	const dirSize, entSize, dataEntSize = 16, 8, 16
	resources := n + 1 // les n images et le groupe
	rootOff := 0
	typeDirOff := rootOff + dirSize + 2*entSize         // RT_ICON puis RT_GROUP_ICON
	iconNamesOff := typeDirOff                          // RT_ICON : n identifiants
	groupNamesOff := iconNamesOff + dirSize + n*entSize // RT_GROUP_ICON : 1 identifiant
	langDirsOff := groupNamesOff + dirSize + entSize    // une par ressource
	dataEntsOff := langDirsOff + resources*(dirSize+entSize)
	dataOff := dataEntsOff + resources*dataEntSize

	blobs := make([][]byte, 0, resources)
	blobs = append(blobs, pngs...)
	blobs = append(blobs, grp.Bytes())
	blobOff := make([]int, resources)
	off := dataOff
	for i, b := range blobs {
		blobOff[i] = off
		off += (len(b) + 3) &^ 3
	}
	size := off

	sec := make([]byte, size)
	put32 := func(at int, v uint32) { le.PutUint32(sec[at:], v) }
	put16 := func(at int, v uint16) { le.PutUint16(sec[at:], v) }
	dir := func(at, ids int) { put16(at+14, uint16(ids)) } // NumberOfIdEntries
	sub := func(off int) uint32 { return 0x80000000 | uint32(off) }

	dir(rootOff, 2)
	put32(rootOff+dirSize, rtIcon)
	put32(rootOff+dirSize+4, sub(iconNamesOff))
	put32(rootOff+dirSize+entSize, rtGroupIcon)
	put32(rootOff+dirSize+entSize+4, sub(groupNamesOff))

	dir(iconNamesOff, n)
	for i := 0; i < n; i++ {
		put32(iconNamesOff+dirSize+i*entSize, uint32(i+1))
		put32(iconNamesOff+dirSize+i*entSize+4, sub(langDirsOff+i*(dirSize+entSize)))
	}
	dir(groupNamesOff, 1)
	put32(groupNamesOff+dirSize, 1)
	put32(groupNamesOff+dirSize+4, sub(langDirsOff+n*(dirSize+entSize)))

	var relocs []uint32 // où écrire l'adresse de chaque entrée de données
	for i := 0; i < resources; i++ {
		ld := langDirsOff + i*(dirSize+entSize)
		dir(ld, 1)
		put32(ld+dirSize, lang)
		put32(ld+dirSize+4, uint32(dataEntsOff+i*dataEntSize)) // feuille : sans le bit de sous-répertoire
		de := dataEntsOff + i*dataEntSize
		put32(de, uint32(blobOff[i])) // OffsetToData : complété par la relocalisation
		put32(de+4, uint32(len(blobs[i])))
		relocs = append(relocs, uint32(de))
		copy(sec[blobOff[i]:], blobs[i])
	}

	// le fichier : en-tête COFF, en-tête de section, données, relocalisations, symboles
	const headSize, secHeadSize, relocSize, symSize = 20, 40, 10, 18
	rawOff := headSize + secHeadSize
	relocOff := rawOff + len(sec)
	symOff := relocOff + len(relocs)*relocSize

	var f bytes.Buffer
	binary.Write(&f, le, struct {
		Machine, Sections          uint16
		Time, SymTable, Symbols    uint32
		OptionalSize, Characterist uint16
	}{0x8664, 1, 0, uint32(symOff), 1, 0, 0})
	var name [8]byte
	copy(name[:], ".rsrc")
	f.Write(name[:])
	binary.Write(&f, le, struct {
		VirtualSize, VirtualAddress, RawSize, RawOff, RelocOff, LineOff uint32
		Relocs, Lines                                                   uint16
		Flags                                                           uint32
	}{0, 0, uint32(len(sec)), uint32(rawOff), uint32(relocOff), 0, uint16(len(relocs)), 0, 0x40000040})
	f.Write(sec)
	for _, at := range relocs {
		binary.Write(&f, le, struct {
			Addr, Symbol uint32
			Type         uint16
		}{at, 0, 3}) // IMAGE_REL_AMD64_ADDR32NB vers le symbole 0 (.rsrc)
	}
	f.Write(name[:]) // le symbole : la section elle-même
	binary.Write(&f, le, struct {
		Value   uint32
		Section uint16
		Type    uint16
		Class   uint8
		Aux     uint8
	}{0, 1, 0, 3, 0}) // IMAGE_SYM_CLASS_STATIC
	binary.Write(&f, le, uint32(4)) // table des chaînes : vide (sa taille seule)
	return f.Bytes()
}
