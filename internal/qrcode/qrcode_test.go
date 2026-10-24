package qrcode

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image/color"
	"image/png"
	"math"
	"strings"
	"testing"

	encoder "github.com/skip2/go-qrcode"
)

func TestStyledAccessLinksPreserveQuietZoneContrastAndModuleCenters(t *testing.T) {
	for _, raw := range []string{
		"https://subscriptions.example/fictional?x=1&y=2",
		"vless://00000000-0000-4000-8000-000000000001@example.invalid:443?security=reality&pbk=" + strings.Repeat("aZ9_-", 200) + "#Москва%20→%20Стокгольм",
		"vless://00000000-0000-4000-8000-000000000001@example.invalid:443?security=reality&pbk=" + strings.Repeat("aZ9_-", 430) + "#Москва%20→%20Стокгольм",
	} {
		data, err := PNG(raw)
		if err != nil {
			t.Fatal("valid access link was not encoded")
		}
		image, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal("QR is not a PNG")
		}
		bounds := image.Bounds()
		if bounds.Dx() < 360 || bounds.Dy() != bounds.Dx() || bounds.Dy() > 1280 {
			t.Fatal("QR must be a pure square within Telegram image dimensions")
		}
		if base64.StdEncoding.EncodedLen(len(data)) > 262144 {
			t.Fatal("QR must fit the Mini App image payload limit")
		}
		at := func(x, y int) color.NRGBA {
			return color.NRGBAModel.Convert(image.At(x, y)).(color.NRGBA)
		}
		if at(0, 0) != (color.NRGBA{R: 184, G: 229, B: 222, A: 255}) || at(bounds.Max.X-1, bounds.Max.Y-1) != (color.NRGBA{R: 195, G: 221, B: 242, A: 255}) {
			t.Fatal("QR background must remain opaque mint-to-ice without a paper frame")
		}
		code, err := encoder.New(raw, encoder.Medium)
		if err != nil {
			t.Fatal("fixture must be encodable")
		}
		bitmap := code.Bitmap()
		size := bounds.Dx()
		scale := size / len(bitmap)
		if scale < 6 || size%len(bitmap) != 0 {
			t.Fatal("modules must retain an integer scale of at least six pixels")
		}
		for y, row := range bitmap {
			for x, wantDark := range row {
				c := at(x*scale+scale/2, y*scale+scale/2)
				luminance := relativeLuminance(c)
				if c.A != 255 || (wantDark && luminance > 0.08) || (!wantDark && luminance < 0.60) {
					t.Fatalf("QR module center (%d,%d): dark=%v, luminance=%.3f, alpha=%d", x, y, wantDark, luminance, c.A)
				}
			}
		}
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				if x >= 4*scale && y >= 4*scale && x < size-4*scale && y < size-4*scale {
					continue
				}
				c := at(x, y)
				if relativeLuminance(c) < 0.60 || c.A != 255 {
					t.Fatal("four-module light quiet zone is missing")
				}
			}
		}
	}
}

func relativeLuminance(c color.NRGBA) float64 {
	linear := func(channel uint8) float64 {
		value := float64(channel) / 255
		if value <= 0.04045 {
			return value / 12.92
		}
		return math.Pow((value+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(c.R) + 0.7152*linear(c.G) + 0.0722*linear(c.B)
}

func TestUnencodableInputFailsWithoutTruncationOrSensitiveErrors(t *testing.T) {
	for _, raw := range []string{"", "private-credential-" + strings.Repeat("x", 3000), strings.Repeat("x", 100000)} {
		data, err := PNG(raw)
		if len(data) != 0 || !errors.Is(err, ErrUnavailable) || err.Error() != "QR code unavailable" {
			t.Fatal("unencodable input must fail with a generic error and no partial image")
		}
	}
}
