// Command brandgen draws Dotlane's mark (G, "period in the bar", Ink & Green) and writes every
// icon file in assets/brand: SVGs, PNGs, favicon.ico and dotlane.icns. The SVGs and the PNGs come
// from the same numbers below, so they cannot drift apart. docs/brand.md describes the result.
//
//	go run ./scripts/brandgen            # from the repository root
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
)

// The palette (docs/brand.md, Colour).
const (
	ink      = "#0f172a" // tile, and the glyph on paper
	inkRim   = "#6f747f" // hairline that keeps the ink tile visible on dark docks
	snow     = "#f8fafc" // glyph on ink
	green    = "#22c55e" // the dot on ink
	paper    = "#ffffff"
	paperRim = "#e2e8f0"
	deep     = "#16a34a" // the dot on paper: a deeper green that holds 3:1 on white
)

// geometry is the mark in a 64-unit box. Small sizes use a bolder cut, so the bar stays at least
// a pixel wide at 16 px.
type geometry struct {
	tileR, rim             float64 // tile corner radius, rim stroke
	barX, barY, barW, barH float64 // the bar's outline, centred on its stroke
	barStroke              float64
	dotX, dotY, dotR       float64
}

var (
	regular = geometry{tileR: 15, rim: 1, barX: 11.5, barY: 23.5, barW: 41, barH: 17, barStroke: 3, dotX: 43.5, dotY: 32, dotR: 3.75}
	small   = geometry{tileR: 14, rim: 2.5, barX: 9, barY: 21.5, barW: 46, barH: 21, barStroke: 5, dotX: 43.5, dotY: 32, dotR: 5}
)

type theme struct{ tile, rim, glyph, dot string }

var (
	inkTheme   = theme{ink, inkRim, snow, green}
	paperTheme = theme{paper, paperRim, ink, deep}
)

func main() {
	out := filepath.Join("assets", "brand")
	must(os.MkdirAll(filepath.Join(out, "png"), 0o755))

	write := func(name string, data []byte) {
		must(os.WriteFile(filepath.Join(out, name), data, 0o644))
		fmt.Println(filepath.Join(out, name))
	}
	write("mark.svg", []byte(svg(regular, inkTheme)))
	write("mark-paper.svg", []byte(svg(regular, paperTheme)))
	write("mark-small.svg", []byte(svg(small, inkTheme)))
	write("favicon.svg", []byte(faviconSVG()))
	// The same favicon, embedded in the pages the daemon serves (internal/proxy/page.html).
	must(os.WriteFile(filepath.Join("internal", "proxy", "favicon.svg"), []byte(faviconSVG()), 0o644))
	write("glyph.svg", []byte(glyphSVG()))

	for _, size := range []int{16, 32, 48, 64, 128, 180, 192, 256, 512, 1024} {
		write(fmt.Sprintf("png/mark-%d.png", size), encode(draw(size, size, inkTheme)))
	}
	for _, size := range []int{32, 64, 256, 512} {
		write(fmt.Sprintf("png/mark-paper-%d.png", size), encode(draw(size, size, paperTheme)))
	}
	write("png/apple-touch-icon.png", encode(draw(180, 180, inkTheme)))

	var icoImages [][]byte
	for _, size := range []int{16, 32, 48} {
		icoImages = append(icoImages, encode(draw(size, size, inkTheme)))
	}
	write("favicon.ico", ico([]int{16, 32, 48}, icoImages))
	write("dotlane.icns", icns())
}

// svg is the mark as a standalone SVG.
func svg(g geometry, t theme) string {
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" role="img" aria-label="Dotlane">
  <rect width="64" height="64" rx="%g" fill="%s"/>
  <rect x="%g" y="%g" width="%g" height="%g" rx="%g" fill="none" stroke="%s" stroke-width="%g"/>
  <rect x="%g" y="%g" width="%g" height="%g" rx="%g" fill="none" stroke="%s" stroke-width="%g"/>
  <circle cx="%g" cy="%g" r="%g" fill="%s"/>
</svg>
`, g.tileR, t.tile,
		g.rim/2, g.rim/2, 64-g.rim, 64-g.rim, g.tileR-g.rim/2, t.rim, g.rim,
		g.barX, g.barY, g.barW, g.barH, g.barH/2, t.glyph, g.barStroke,
		g.dotX, g.dotY, g.dotR, t.dot)
}

// faviconSVG follows the browser's theme: the paper mark in light mode, the ink mark in dark.
// It uses the small cut, since a favicon is shown at 16–32 px.
func faviconSVG() string {
	g := small
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">
  <style>
    .tile{fill:%[1]s}.rim{stroke:%[2]s}.bar{stroke:%[3]s}.dot{fill:%[4]s}
    @media (prefers-color-scheme: dark){.tile{fill:%[5]s}.rim{stroke:%[6]s}.bar{stroke:%[7]s}.dot{fill:%[8]s}}
  </style>
  <rect class="tile" width="64" height="64" rx="%[9]g"/>
  <rect class="rim" x="%[10]g" y="%[10]g" width="%[11]g" height="%[11]g" rx="%[12]g" fill="none" stroke-width="%[13]g"/>
  <rect class="bar" x="%[14]g" y="%[15]g" width="%[16]g" height="%[17]g" rx="%[18]g" fill="none" stroke-width="%[19]g"/>
  <circle class="dot" cx="%[20]g" cy="%[21]g" r="%[22]g"/>
</svg>
`, paper, paperRim, ink, deep, ink, inkRim, snow, green,
		g.tileR, g.rim/2, 64-g.rim, g.tileR-g.rim/2, g.rim,
		g.barX, g.barY, g.barW, g.barH, g.barH/2, g.barStroke,
		g.dotX, g.dotY, g.dotR)
}

// glyphSVG is the mark without its tile, for use inline next to text: the bar takes the text
// colour (currentColor), the dot stays green.
func glyphSVG() string {
	g := regular
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="8 20 48 24" role="img" aria-label="Dotlane">
  <rect x="%g" y="%g" width="%g" height="%g" rx="%g" fill="none" stroke="currentColor" stroke-width="%g"/>
  <circle cx="%g" cy="%g" r="%g" fill="%s"/>
</svg>
`, g.barX, g.barY, g.barW, g.barH, g.barH/2, g.barStroke, g.dotX, g.dotY, g.dotR, deep)
}

// draw renders the mark centred in a w×h canvas, the tile filling the shorter side. Sizes of
// 32 px and below use the small cut.
func draw(w, h int, t theme) *image.NRGBA {
	side := min(w, h)
	g := regular
	if side <= 32 {
		g = small
	}
	return render(w, h, float64(side), g, t)
}

// render draws the mark with its tile `side` pixels wide, centred, by supersampling each pixel.
func render(w, h int, side float64, g geometry, t theme) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	scale := side / 64
	ox, oy := (float64(w)-side)/2, (float64(h)-side)/2
	tile, rim, glyph, dot := hex(t.tile), hex(t.rim), hex(t.glyph), hex(t.dot)
	const n = 8 // samples per axis
	for py := 0; py < h; py++ {
		for px := 0; px < w; px++ {
			var r, gg, b, a float64
			for sy := 0; sy < n; sy++ {
				for sx := 0; sx < n; sx++ {
					x := (float64(px) + (float64(sx)+0.5)/n - ox) / scale
					y := (float64(py) + (float64(sy)+0.5)/n - oy) / scale
					c, ok := sample(x, y, g, tile, rim, glyph, dot)
					if ok {
						r += float64(c.R)
						gg += float64(c.G)
						b += float64(c.B)
						a++
					}
				}
			}
			if a == 0 {
				continue
			}
			img.SetNRGBA(px, py, color.NRGBA{
				R: uint8(math.Round(r / a)), G: uint8(math.Round(gg / a)), B: uint8(math.Round(b / a)),
				A: uint8(math.Round(255 * a / (n * n))),
			})
		}
	}
	return img
}

// sample returns the colour at (x, y) in mark units, top layer first; false outside the tile.
func sample(x, y float64, g geometry, tile, rim, glyph, dot color.NRGBA) (color.NRGBA, bool) {
	if roundRect(x, y, 0, 0, 64, 64, g.tileR) > 0 {
		return color.NRGBA{}, false
	}
	if math.Hypot(x-g.dotX, y-g.dotY) <= g.dotR {
		return dot, true
	}
	if math.Abs(roundRect(x, y, g.barX, g.barY, g.barW, g.barH, g.barH/2)) <= g.barStroke/2 {
		return glyph, true
	}
	inset := g.rim / 2
	if math.Abs(roundRect(x, y, inset, inset, 64-g.rim, 64-g.rim, g.tileR-inset)) <= g.rim/2 {
		return rim, true
	}
	return tile, true
}

// roundRect is the signed distance from (px, py) to a rounded rectangle: negative inside.
func roundRect(px, py, x, y, w, h, r float64) float64 {
	cx, cy := x+w/2, y+h/2
	qx := math.Abs(px-cx) - w/2 + r
	qy := math.Abs(py-cy) - h/2 + r
	outside := math.Hypot(math.Max(qx, 0), math.Max(qy, 0))
	return outside + math.Min(math.Max(qx, qy), 0) - r
}

func hex(s string) color.NRGBA {
	var c color.NRGBA
	fmt.Sscanf(s, "#%02x%02x%02x", &c.R, &c.G, &c.B)
	c.A = 255
	return c
}

func encode(img image.Image) []byte {
	var b bytes.Buffer
	must(png.Encode(&b, img))
	return b.Bytes()
}

// ico packs PNG images into a .ico file (PNG entries are valid since Windows Vista).
func ico(sizes []int, images [][]byte) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, [3]uint16{0, 1, uint16(len(images))})
	offset := 6 + 16*len(images)
	for i, data := range images {
		dim := uint8(sizes[i] % 256) // 0 means 256
		binary.Write(&b, binary.LittleEndian, struct {
			W, H, Colors, Reserved uint8
			Planes, BPP            uint16
			Size, Offset           uint32
		}{dim, dim, 0, 0, 1, 32, uint32(len(data)), uint32(offset)})
		offset += len(data)
	}
	for _, data := range images {
		b.Write(data)
	}
	return b.Bytes()
}

// icns packs the macOS icon. Following Apple's icon grid, the tile is 824/1024 of the canvas,
// centred, so it sits in line with other apps in the Dock and Finder.
func icns() []byte {
	entries := []struct {
		kind string
		size int
	}{
		{"icp4", 16}, {"icp5", 32}, {"ic11", 32}, {"icp6", 48}, {"ic12", 64},
		{"ic07", 128}, {"ic08", 256}, {"ic13", 256}, {"ic09", 512}, {"ic14", 512}, {"ic10", 1024},
	}
	var body bytes.Buffer
	for _, e := range entries {
		side := math.Round(float64(e.size) * 824 / 1024)
		g := regular
		if e.size <= 32 {
			g = small
		}
		data := encode(render(e.size, e.size, side, g, inkTheme))
		body.WriteString(e.kind)
		binary.Write(&body, binary.BigEndian, uint32(8+len(data)))
		body.Write(data)
	}
	var b bytes.Buffer
	b.WriteString("icns")
	binary.Write(&b, binary.BigEndian, uint32(8+body.Len()))
	b.Write(body.Bytes())
	return b.Bytes()
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
