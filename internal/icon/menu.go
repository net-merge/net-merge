package icon

import "math"

// menuGlyph is the line drawing for a tray menu row that is not an adapter, in the same 32px square
// as the adapter glyphs, or nil for any other name. Adapter turns it into the platform's format.
func menuGlyph(name string) (lines [][4]float64) {
	line := func(x1, y1, x2, y2 float64) { lines = append(lines, [4]float64{x1, y1, x2, y2}) }
	// arc runs clockwise on screen from one angle to another; 0 degrees points right, 90 down.
	arc := func(cx, cy, rx, ry, from, to float64) {
		const n = 32
		for i := range n {
			a := (from + (to-from)*float64(i)/n) * math.Pi / 180
			b := (from + (to-from)*float64(i+1)/n) * math.Pi / 180
			line(cx+rx*math.Cos(a), cy+ry*math.Sin(a), cx+rx*math.Cos(b), cy+ry*math.Sin(b))
		}
	}
	circle := func(cx, cy, r float64) { arc(cx, cy, r, r, 0, 360) }
	switch name {
	case "speed":
		arc(16, 20, 12, 12, 160, 380)
		line(16, 20, 22, 13)
	case "globe":
		circle(16, 16, 12)
		arc(16, 16, 5.5, 12, 0, 360)
		line(4, 16, 28, 16)
	case "game":
		line(9, 9, 23, 9)
		arc(23, 16, 7, 7, -90, 90)
		line(23, 23, 9, 23)
		arc(9, 16, 7, 7, 90, 270)
		line(7, 16, 13, 16)
		line(10, 13, 10, 19)
		line(21, 14, 21, 14)
		line(24, 18, 24, 18)
	case "link":
		line(10, 11, 14, 11)
		arc(10, 16, 5, 5, 90, 270)
		line(10, 21, 14, 21)
		line(18, 11, 22, 11)
		arc(22, 16, 5, 5, -90, 90)
		line(18, 21, 22, 21)
		line(11, 16, 21, 16)
	case "cool": // a thermometer
		arc(16, 7, 3, 3, 180, 360)
		line(13, 7, 13, 19)
		line(19, 7, 19, 19)
		arc(16, 23, 5.5, 5.5, -57, 237)
		line(16, 13, 16, 23)
	case "person":
		circle(16, 10, 6)
		arc(16, 28, 11, 9, 180, 360)
	case "play":
		line(10, 6, 26, 16)
		line(26, 16, 10, 26)
		line(10, 26, 10, 6)
	case "pause":
		line(11, 7, 11, 25)
		line(21, 7, 21, 25)
	case "info":
		circle(16, 16, 12)
		line(16, 15, 16, 22)
		line(16, 10, 16, 10)
	case "power":
		arc(16, 17, 11, 11, -55, 235)
		line(16, 3, 16, 15)
	}
	return lines
}
