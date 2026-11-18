package icon

import (
	"bytes"
	"encoding/binary"
	"image/color"
	"image/png"
	"runtime"
	"testing"
)

func TestAdapterIconsUseNativeFormatsAndTemplates(t *testing.T) {
	seen := map[string]bool{}
	for _, kind := range []string{"wifi", "ethernet", "phone", "network"} {
		template, regular := Adapter(kind)
		if seen[string(regular)] {
			t.Fatalf("%s has the same glyph as another adapter type", kind)
		}
		seen[string(regular)] = true
		if runtime.GOOS == "windows" {
			if len(regular) < 22 || binary.LittleEndian.Uint16(regular[2:4]) != 1 {
				t.Fatal("Windows needs an ICO container")
			}
			regular = regular[22:]
		}
		for _, b := range [][]byte{template, regular} {
			img, err := png.Decode(bytes.NewReader(b))
			if err != nil || img.Bounds().Dx() != 32 || img.Bounds().Dy() != 32 {
				t.Fatalf("invalid glyph: %v", err)
			}
		}
		img, _ := png.Decode(bytes.NewReader(template))
		for y := 0; y < 32; y++ {
			for x := 0; x < 32; x++ {
				c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
				if c.R != 0 || c.G != 0 || c.B != 0 {
					t.Fatal("macOS template must be monochrome")
				}
			}
		}
	}
}
