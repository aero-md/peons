// Package gears joue l'animation de /resume : deux engrenages identiques, couplés,
// dents emboîtées, qui tournent en sens contraires au-dessus de la tête du clawd. Le
// second est posé en diagonale par rapport au premier.
//
// Les engrenages sont des images PNG (assets/fx/gears_NN.png, dessinées par
// tools/gear-fx) : une par position de rotation, Frames positions pour un pas de dent.
// Les roues sont à 8 dents, donc après un pas de dent le dessin est le même qu'au départ
// et la boucle de Frames images couvre toute la rotation. Ce paquet ne fait que choisir,
// à chaque instant, l'image qui correspond à l'angle des roues : elles accélèrent, tournent,
// freinent et s'arrêtent.
package gears

import (
	"fmt"
	"image"
	"io/fs"
	"math"
	"time"

	"peons/internal/avatar"
)

const (
	// Teeth : dents par roue. Un pas de dent = 2π/Teeth.
	Teeth = 8
	// Frames : nombre d'images (de positions) pour un pas de dent. Une dent avance de
	// 6,3 px d'art à la tête sur un pas : avec 10 images, de 0,6 px d'une image à la
	// suivante, ce qui change toujours quelques pixels (jusqu'à 20 images, deux voisines
	// restent différentes) sans multiplier les fichiers. Surtout, à pleine vitesse les roues
	// avancent de 0,2 pas de dent par Step : 2 images exactement, une cadence régulière.
	// Avec 12 ou 14, on alternerait 2 et 3 images d'un Step à l'autre : un tressautement.
	// 5 serait régulier aussi, mais trop grossier pour l'accélération et le freinage.
	Frames = 10

	// Duration : durée de l'animation. La vitesse monte (rampUp), tient, puis retombe
	// (rampDown) jusqu'à l'arrêt, et les roues disparaissent.
	Duration = 2400 * time.Millisecond
	// Step : on regarde où en sont les roues toutes les Step (20 fois par seconde : assez
	// pour que les dents ne sautent pas, peu pour que la fenêtre se redessine rarement).
	Step     = 50 * time.Millisecond
	rampUp   = 350 * time.Millisecond
	rampDown = 550 * time.Millisecond

	revPerSec = 0.5 // tours par seconde des roues, à pleine vitesse (9° par Step)
)

// File : le chemin, dans le système de fichiers des assets, de l'image de la position k
// (0 à Frames-1).
func File(k int) string { return fmt.Sprintf("%s/gears_%02d.png", avatar.FXDir, k+1) }

// Animation : les images des positions de rotation, chargées une fois pour toutes.
type Animation struct {
	Frames []*image.NRGBA
}

// Load lit les Frames images des engrenages dans fsys.
func Load(fsys fs.FS) (*Animation, error) {
	an := &Animation{Frames: make([]*image.NRGBA, Frames)}
	for k := range an.Frames {
		img, err := avatar.LoadImage(fsys, File(k))
		if err != nil {
			return nil, fmt.Errorf("engrenages : %w", err)
		}
		if k > 0 && img.Rect.Size() != an.Frames[0].Rect.Size() {
			return nil, fmt.Errorf("engrenages : %s fait %v, la première %v", File(k), img.Rect.Size(), an.Frames[0].Rect.Size())
		}
		an.Frames[k] = img
	}
	return an, nil
}

// Size : taille d'une image, en pixels d'art (zéro si l'animation est vide).
func (an *Animation) Size() image.Point {
	if len(an.Frames) == 0 {
		return image.Point{}
	}
	return an.Frames[0].Rect.Size()
}

// Frame donne la position à montrer à l'instant elapsed depuis le début, ou false une
// fois l'animation finie.
func (an *Animation) Frame(elapsed time.Duration) (int, bool) {
	if elapsed < 0 || elapsed >= Duration || len(an.Frames) == 0 {
		return 0, false
	}
	turns := angleAt(elapsed) / (2 * math.Pi) // tours de la 1re roue
	k := int(math.Round(turns*Teeth*Frames)) % Frames
	return k, true
}

// NextFrame : l'instant (depuis le début) où l'on reregarde les roues après elapsed.
func NextFrame(elapsed time.Duration) time.Duration {
	return (elapsed/Step + 1) * Step
}

// speed : la vitesse relative (0 à 1) à l'instant t.
func speed(t time.Duration) float64 {
	smooth := func(x float64) float64 { x = math.Max(0, math.Min(1, x)); return x * x * (3 - 2*x) }
	return smooth(float64(t)/float64(rampUp)) * smooth(float64(Duration-t)/float64(rampDown))
}

// angleAt : l'angle de la grande roue à l'instant t, intégrale de la vitesse
// (calculée au pas fin : l'accélération et le freinage sont continus).
func angleAt(t time.Duration) float64 {
	const dt = time.Millisecond
	var turns float64
	for u := time.Duration(0); u < t; u += dt {
		turns += speed(u+dt/2) * revPerSec * dt.Seconds()
	}
	return 2 * math.Pi * turns
}
