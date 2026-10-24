// Package qrcode renders private access links entirely in memory.
package qrcode

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"

	encoder "github.com/skip2/go-qrcode"
)

// ErrUnavailable deliberately carries no credential or encoder details.
var ErrUnavailable = errors.New("QR code unavailable")

// PNG encodes the exact input as a branded PNG with a four-module quiet zone.
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
	const padding, header, footer = 20, 72, 24
	canvas := image.NewNRGBA(image.Rect(0, 0, size+2*padding, size+header+footer))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(cardColor), image.Point{}, draw.Src)
	panelX, panelY := padding, header
	roundedRect(canvas, float64(panelX), float64(panelY), float64(size), float64(size), 16, panelColor)
	drawBrand(canvas, canvas.Bounds().Dx()/2, 23)
	for y, row := range bitmap {
		for x, dark := range row {
			if !dark || inFinder(x, y, modules) {
				continue
			}
			roundedRect(canvas, float64(panelX+x*scale)+float64(scale)*0.05, float64(panelY+y*scale)+float64(scale)*0.05, float64(scale)*0.9, float64(scale)*0.9, float64(scale)*0.43, inkColor)
		}
	}
	for i, pos := range [][2]int{{4, 4}, {modules - 11, 4}, {4, modules - 11}} {
		x, y, unit := float64(panelX+pos[0]*scale), float64(panelY+pos[1]*scale), float64(scale)
		roundedRect(canvas, x, y, 7*unit, 7*unit, 1.4*unit, inkColor)
		roundedRect(canvas, x+unit, y+unit, 5*unit, 5*unit, 0.9*unit, panelColor)
		accent := []color.NRGBA{tealColor, terraColor, inkColor}[i]
		roundedRect(canvas, x+2*unit, y+2*unit, 3*unit, 3*unit, 0.8*unit, accent)
	}
	var output bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&output, canvas); err != nil {
		return nil, ErrUnavailable
	}
	return output.Bytes(), nil
}

var (
	cardColor  = color.NRGBA{R: 247, G: 244, B: 237, A: 255}
	panelColor = color.NRGBA{R: 255, G: 253, B: 249, A: 255}
	inkColor   = color.NRGBA{R: 23, G: 42, B: 56, A: 255}
	tealColor  = color.NRGBA{R: 18, G: 91, B: 91, A: 255}
	terraColor = color.NRGBA{R: 165, G: 68, B: 47, A: 255}
)

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
			mix := func(a, b uint8) uint8 { return uint8((int(a)*covered + int(b)*(16-covered) + 8) / 16) }
			dst.SetNRGBA(px, py, color.NRGBA{R: mix(fill.R, base.R), G: mix(fill.G, base.G), B: mix(fill.B, base.B), A: 255})
		}
	}
}

// A small geometric wordmark avoids platform fonts or additional dependencies.
func drawBrand(dst *image.NRGBA, center, top int) {
	const width, height, gap, stroke = 20, 26, 8, 3
	left := center - (6*width+5*gap)/2
	line := func(x1, y1, x2, y2 float64) {
		length := math.Hypot(x2-x1, y2-y1)
		steps := int(math.Ceil(length * 2))
		for step := 0; step <= steps; step++ {
			t := float64(step) / float64(max(steps, 1))
			roundedRect(dst, x1+(x2-x1)*t-stroke/2.0, y1+(y2-y1)*t-stroke/2.0, stroke, stroke, stroke/2.0, inkColor)
		}
	}
	for i, letter := range "OTVALI" {
		x, y := float64(left+i*(width+gap)), float64(top)
		switch letter {
		case 'O':
			roundedRect(dst, x, y, width, height, 10, inkColor)
			roundedRect(dst, x+stroke, y+stroke, width-2*stroke, height-2*stroke, 7, cardColor)
		case 'T':
			line(x, y+1.5, x+width, y+1.5)
			line(x+width/2, y+1.5, x+width/2, y+height-1.5)
		case 'V':
			line(x, y+1.5, x+width/2, y+height-1.5)
			line(x+width/2, y+height-1.5, x+width, y+1.5)
		case 'A':
			line(x, y+height-1.5, x+width/2, y+1.5)
			line(x+width/2, y+1.5, x+width, y+height-1.5)
			line(x+4, y+17, x+width-4, y+17)
		case 'L':
			line(x+1.5, y+1.5, x+1.5, y+height-1.5)
			line(x+1.5, y+height-1.5, x+width, y+height-1.5)
		case 'I':
			line(x+width/2, y+1.5, x+width/2, y+height-1.5)
		}
	}
}
