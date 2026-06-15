package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

func main() {
	// Test data
	date := "Rabu, 10 Juni 2026"
	// Use a generic placeholder image URL or a real one if available
	screenshotURL := "https://pub-cfeca4b1940f402fba2229be85fdac9c.r2.dev/dnarmasid/screenshots/test.png"

	// Read template
	tplPath := "/app/services/media-generator/templates/heroScreenshotTemplate.html"
	if _, err := os.Stat(tplPath); os.IsNotExist(err) {
		tplPath = "services/media-generator/templates/heroScreenshotTemplate.html"
	}
	
	tpl, err := os.ReadFile(tplPath)
	if err != nil {
		log.Fatalf("Read template: %v", err)
	}

	// Replace placeholders
	html := string(tpl)
	html = strings.ReplaceAll(html, "{{date}}", date)
	html = strings.ReplaceAll(html, "{{screenshot_url}}", screenshotURL)

	// Write temp HTML
	tmpHTML := "/tmp/test-hero-slide.html"
	err = os.WriteFile(tmpHTML, []byte(html), 0644)
	if err != nil {
		log.Fatalf("Write temp HTML: %v", err)
	}

	// Setup chromedp
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-gpu", true),
	)
	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	// Render to PNG
	outPath := "/tmp/test-hero-slide.png"
	var buf []byte
	err = chromedp.Run(ctx,
		chromedp.Navigate("file://"+tmpHTML),
		chromedp.Sleep(2*time.Second), // Wait for fonts/render
		chromedp.FullScreenshot(&buf, 100),
	)
	if err != nil {
		log.Fatalf("Render: %v", err)
	}

	err = os.WriteFile(outPath, buf, 0644)
	if err != nil {
		log.Fatalf("Write PNG: %v", err)
	}

	fmt.Printf("✅ Rendered: %s\n", outPath)
}
