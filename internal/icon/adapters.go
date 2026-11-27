package icon

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"runtime"
)

// Adapter returns a black macOS template and a regular platform-format icon.
// The four original glyphs are compiled into the binary; no images are downloaded.
func Adapter(kind string) (template, regular []byte) {
	return adapterPNG(kind, color.NRGBA{A: 255}), adapterRegular(kind)
}

func adapterRegular(kind string) []byte {
	p := adapterPNG(kind, color.NRGBA{R: 40, G: 120, B: 210, A: 255})
	if runtime.GOOS != "windows" {
		return p
	}
	var ico bytes.Buffer
	binary.Write(&ico, binary.LittleEndian, [3]uint16{0, 1, 1})
	ico.Write([]byte{size, size, 0, 0})
	binary.Write(&ico, binary.LittleEndian, [2]uint16{1, 32})
	binary.Write(&ico, binary.LittleEndian, uint32(len(p)))
	binary.Write(&ico, binary.LittleEndian, uint32(22))
	ico.Write(p)
	return ico.Bytes()
}

func adapterPNG(kind string, ink color.NRGBA) []byte {
	// Coordinates are in a 32px square. Coverage gives small menu glyphs smooth edges.
	var lines [][4]float64
	switch kind {
	case "wifi":
		for _, radius := range []float64{7, 13, 19} {
			for i := 0; i < 24; i++ {
				a, b := -math.Pi*3/4+float64(i)*math.Pi/48, -math.Pi*3/4+float64(i+1)*math.Pi/48
				lines = append(lines, [4]float64{16 + radius*math.Cos(a), 27 + radius*math.Sin(a), 16 + radius*math.Cos(b), 27 + radius*math.Sin(b)})
			}
		}
		lines = append(lines, [4]float64{16, 26, 16, 27})
	case "ethernet":
		lines = [][4]float64{{5, 7, 27, 7}, {5, 7, 5, 22}, {27, 7, 27, 22}, {5, 22, 11, 22}, {27, 22, 21, 22}, {11, 22, 11, 26}, {21, 22, 21, 26}, {11, 26, 21, 26}, {10, 8, 10, 14}, {14, 8, 14, 14}, {18, 8, 18, 14}, {22, 8, 22, 14}}
	case "phone":
		lines = [][4]float64{{9, 4, 23, 4}, {23, 4, 23, 28}, {23, 28, 9, 28}, {9, 28, 9, 4}, {13, 8, 19, 8}, {14, 24, 18, 24}}
	default:
		lines = [][4]float64{{11, 5, 21, 5}, {21, 5, 21, 14}, {21, 14, 11, 14}, {11, 14, 11, 5}, {16, 14, 16, 20}, {6, 20, 26, 20}, {6, 20, 6, 24}, {26, 20, 26, 24}, {2, 25, 10, 25}, {10, 25, 10, 29}, {10, 29, 2, 29}, {2, 29, 2, 25}, {22, 25, 30, 25}, {30, 25, 30, 29}, {30, 29, 22, 29}, {22, 29, 22, 25}}
	}
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			coverage := 0.0
			for _, s := range lines {
				coverage = math.Max(coverage, clamp01(1.25-distToSeg(float64(x)+0.5, float64(y)+0.5, s[0], s[1], s[2], s[3])+0.5))
			}
			c := ink
			c.A = uint8(coverage * float64(c.A))
			img.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}
