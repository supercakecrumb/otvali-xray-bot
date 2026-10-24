// Package qrcode renders private access links entirely in memory.
package qrcode

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"

	encoder "github.com/skip2/go-qrcode"
)

// ErrUnavailable deliberately carries no credential or encoder details.
var ErrUnavailable = errors.New("QR code unavailable")

// PNG encodes the exact input as a square PNG with a four-module quiet zone.
// Rounded modules retain their centers and finders retain their scanline ratios.
// Inputs that cannot fit are rejected rather than shortened.
func PNG(raw string) ([]byte, error) {
	if raw == "" || len(raw) > 4096 {
		return nil, ErrUnavailable
	}
	code, err := encoder.New(raw, encoder.Medium)
	if err != nil {
		return nil, ErrUnavailable
	}
	// A version v symbol has 17+4*v modules, plus 4 on each side.
	modules := 25 + 4*code.VersionNumber
	scale := max(6, (360+modules-1)/modules)
	bitmap := code.Bitmap()
	size := modules * scale
	canvas := image.NewNRGBA(image.Rect(0, 0, size, size))
	background := func(x, y int) color.NRGBA { return backgroundColor(x, y, size) }
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			canvas.SetNRGBA(x, y, background(x, y))
		}
	}
	for y, row := range bitmap {
		for x, dark := range row {
			if !dark || inFinder(x, y, modules) {
				continue
			}
			roundedRect(canvas, float64(x*scale)+float64(scale)*0.05, float64(y*scale)+float64(scale)*0.05, float64(scale)*0.9, float64(scale)*0.9, float64(scale)*0.43, inkColor)
		}
	}
	for i, pos := range [][2]int{{4, 4}, {modules - 11, 4}, {4, modules - 11}} {
		x, y, unit := float64(pos[0]*scale), float64(pos[1]*scale), float64(scale)
		roundedRect(canvas, x, y, 7*unit, 7*unit, 1.2*unit, inkColor)
		paintRoundedRect(canvas, x+unit, y+unit, 5*unit, 5*unit, 0.9*unit, background)
		accent := []color.NRGBA{tealColor, blueColor, tealColor}[i]
		roundedRect(canvas, x+2*unit, y+2*unit, 3*unit, 3*unit, 0.8*unit, accent)
	}
	var output bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&output, canvas); err != nil {
		return nil, ErrUnavailable
	}
	return output.Bytes(), nil
}

var (
	mintColor = color.NRGBA{R: 184, G: 229, B: 222, A: 255}
	iceColor  = color.NRGBA{R: 195, G: 221, B: 242, A: 255}
	inkColor  = color.NRGBA{R: 17, G: 48, B: 73, A: 255}
	tealColor = color.NRGBA{R: 13, G: 76, B: 87, A: 255}
	blueColor = color.NRGBA{R: 24, G: 68, B: 110, A: 255}
)

func backgroundColor(x, y, size int) color.NRGBA {
	// One continuous, light diagonal gradient also fills finder cutouts and the
	// complete quiet zone. It never changes the dark/light QR module classes.
	position, total := x+y, 2*max(size-1, 1)
	mix := func(a, b uint8) uint8 {
		return uint8((int(a)*(total-position) + int(b)*position + total/2) / total)
	}
	return color.NRGBA{R: mix(mintColor.R, iceColor.R), G: mix(mintColor.G, iceColor.G), B: mix(mintColor.B, iceColor.B), A: 255}
}

func inFinder(x, y, modules int) bool {
	for _, pos := range [][2]int{{4, 4}, {modules - 11, 4}, {4, modules - 11}} {
		if x >= pos[0] && x < pos[0]+7 && y >= pos[1] && y < pos[1]+7 {
			return true
		}
	}
	return false
}

// Supersampling only shape edges gives smooth corners without blurring the QR.
func roundedRect(dst *image.NRGBA, x, y, width, height, radius float64, fill color.NRGBA) {
	paintRoundedRect(dst, x, y, width, height, radius, func(int, int) color.NRGBA { return fill })
}

func paintRoundedRect(dst *image.NRGBA, x, y, width, height, radius float64, fill func(int, int) color.NRGBA) {
	radius = min(radius, min(width, height)/2)
	for py := max(0, int(math.Floor(y))); py < min(dst.Bounds().Max.Y, int(math.Ceil(y+height))); py++ {
		for px := max(0, int(math.Floor(x))); px < min(dst.Bounds().Max.X, int(math.Ceil(x+width))); px++ {
			covered := 0
			for sy := 0; sy < 4; sy++ {
				for sx := 0; sx < 4; sx++ {
					cx, cy := float64(px)+(float64(sx)+0.5)/4, float64(py)+(float64(sy)+0.5)/4
					if cx < x || cy < y || cx >= x+width || cy >= y+height {
						continue
					}
					dx := math.Max(math.Max(x+radius-cx, cx-(x+width-radius)), 0)
					dy := math.Max(math.Max(y+radius-cy, cy-(y+height-radius)), 0)
					if dx*dx+dy*dy <= radius*radius {
						covered++
					}
				}
			}
			if covered == 0 {
				continue
			}
			base := dst.NRGBAAt(px, py)
			shade := fill(px, py)
			mix := func(a, b uint8) uint8 { return uint8((int(a)*covered + int(b)*(16-covered) + 8) / 16) }
			dst.SetNRGBA(px, py, color.NRGBA{R: mix(shade.R, base.R), G: mix(shade.G, base.G), B: mix(shade.B, base.B), A: 255})
		}
	}
}
