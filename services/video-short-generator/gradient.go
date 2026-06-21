package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

// generateGradientPNG creates a 1080x1920 luxury gold gradient background
func generateGradientPNG(outputDir string) (string, error) {
	path := filepath.Join(outputDir, "gradient-bg.png")

	// Skip if already exists
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}

	w, h := 1080, 1920
	img := image.NewRGBA(image.Rect(0, 0, w, h))

	for y := 0; y < h; y++ {
		t := float64(y) / float64(h)

		// Vertical gradient: light cream → warm gold → deeper gold
		// Matches daily pipeline radial gradient palette
		// #F5E8D3 (245,232,211) → #E3CFA8 (227,207,168) → #C2A67A (194,166,122)
		topR, topG, topB := 245.0, 232.0, 211.0    // light cream
		midR, midG, midB := 227.0, 207.0, 168.0    // warm gold
		botR, botG, botB := 194.0, 166.0, 122.0    // deeper gold

		for x := 0; x < w; x++ {
			// Subtle horizontal sine wave for organic texture
			wave := math.Sin(3*math.Pi*float64(x)/float64(w)) * 0.08

			// Two-stop gradient: top→mid for first half, mid→bot for second half
			var r, g, b float64
			if t < 0.5 {
				localT := t * 2.0
				r = topR + (midR-topR)*localT + wave*15
				g = topG + (midG-topG)*localT + wave*12
				b = topB + (midB-topB)*localT + wave*8
			} else {
				localT := (t - 0.5) * 2.0
				r = midR + (botR-midR)*localT + wave*15
				g = midG + (botG-midG)*localT + wave*12
				b = midB + (botB-midB)*localT + wave*8
			}

			r = math.Max(0, math.Min(255, r))
			g = math.Max(0, math.Min(255, g))
			b = math.Max(0, math.Min(255, b))

			img.Set(x, y, color.RGBA{uint8(r), uint8(g), uint8(b), 255})
		}
	}

	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	return path, png.Encode(f, img)
}
