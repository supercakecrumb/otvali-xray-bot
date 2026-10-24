package qrcode

import (
	"bytes"
	"errors"
	"image/color"
	"image/png"
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
		if bounds.Dx() < 400 || bounds.Dy() != bounds.Dx()+56 || bounds.Dy() > 1280 {
			t.Fatal("branded card must stay readable and within Telegram image dimensions")
		}
		code, err := encoder.New(raw, encoder.Medium)
		if err != nil {
			t.Fatal("fixture must be encodable")
		}
		bitmap := code.Bitmap()
		size := bounds.Dx() - 40
		scale := size / len(bitmap)
		if scale < 6 || size%len(bitmap) != 0 {
			t.Fatal("modules must retain an integer scale of at least six pixels")
		}
		for y, row := range bitmap {
			for x, wantDark := range row {
				c := color.NRGBAModel.Convert(image.At(20+x*scale+scale/2, 72+y*scale+scale/2)).(color.NRGBA)
				dark := (int(c.R)+int(c.G)+int(c.B))/3 < 150
				if c.A != 255 || dark != wantDark {
					t.Fatal("styling changed a QR module center or its contrast")
				}
			}
		}
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				if x >= 4*scale && y >= 4*scale && x < size-4*scale && y < size-4*scale {
					continue
				}
				c := color.NRGBAModel.Convert(image.At(20+x, 72+y)).(color.NRGBA)
				if c.R < 230 || c.G < 230 || c.B < 230 || c.A != 255 {
					t.Fatal("four-module light quiet zone is missing")
				}
			}
		}
	}
}

func TestUnencodableInputFailsWithoutTruncationOrSensitiveErrors(t *testing.T) {
	for _, raw := range []string{"", "private-credential-" + strings.Repeat("x", 3000), strings.Repeat("x", 100000)} {
		data, err := PNG(raw)
		if len(data) != 0 || !errors.Is(err, ErrUnavailable) || err.Error() != "QR code unavailable" {
			t.Fatal("unencodable input must fail with a generic error and no partial image")
		}
	}
}
