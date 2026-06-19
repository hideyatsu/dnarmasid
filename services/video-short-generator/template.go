package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"dnarmasid/shared/models"
)

// TemplateRenderer renders HTML templates to PNG
type TemplateRenderer struct {
	templateDir string
	outputDir   string
}

func NewTemplateRenderer(templateDir, outputDir string) *TemplateRenderer {
	ensureDir(outputDir)
	return &TemplateRenderer{templateDir: templateDir, outputDir: outputDir}
}

func (r *TemplateRenderer) RenderHook(date, badge, title, subtitle string) (string, error) {
	html, err := os.ReadFile(filepath.Join(r.templateDir, "hookTemplate.html"))
	if err != nil {
		return "", fmt.Errorf("read hook template: %w", err)
	}
	content := string(html)
	content = strings.ReplaceAll(content, "{{date}}", date)
	content = strings.ReplaceAll(content, "{{badge}}", badge)
	content = strings.ReplaceAll(content, "{{title}}", title)
	content = strings.ReplaceAll(content, "{{subtitle}}", subtitle)
	return r.renderToPNG(content, "hook.png")
}

func (r *TemplateRenderer) RenderPrice(event *models.GoldScrapedEvent, hargaJual, hargaBuyback int64, spreadPct float64) (string, error) {
	html, err := os.ReadFile(filepath.Join(r.templateDir, "priceTemplate.html"))
	if err != nil {
		return "", fmt.Errorf("read price template: %w", err)
	}

	delta := event.ChangeAmt
	deltaClass := "neutral"
	deltaArrow := "→"
	if delta > 0 {
		deltaClass = "up"
		deltaArrow = "▲"
	} else if delta < 0 {
		deltaClass = "down"
		deltaArrow = "▼"
	}

	content := string(html)
	content = strings.ReplaceAll(content, "{{date}}", event.Date)
	content = strings.ReplaceAll(content, "{{harga_jual}}", formatRupiah(hargaJual))
	content = strings.ReplaceAll(content, "{{harga_buyback}}", formatRupiah(hargaBuyback))
	content = strings.ReplaceAll(content, "{{spread}}", fmt.Sprintf("%.1f", spreadPct))
	content = strings.ReplaceAll(content, "{{delta_class}}", deltaClass)
	content = strings.ReplaceAll(content, "{{delta_arrow}}", deltaArrow)
	content = strings.ReplaceAll(content, "{{delta}}", formatRupiah(abs(delta)))
	return r.renderToPNG(content, "price.png")
}

func (r *TemplateRenderer) RenderCTA() (string, error) {
	html, err := os.ReadFile(filepath.Join(r.templateDir, "ctaTemplate.html"))
	if err != nil {
		return "", fmt.Errorf("read cta template: %w", err)
	}
	return r.renderToPNG(string(html), "cta.png")
}

func (r *TemplateRenderer) renderToPNG(htmlContent, outputName string) (string, error) {
	tmpHTML := filepath.Join(r.outputDir, strings.TrimSuffix(outputName, ".png")+".html")
	if err := os.WriteFile(tmpHTML, []byte(htmlContent), 0644); err != nil {
		return "", fmt.Errorf("write temp html: %w", err)
	}
	defer os.Remove(tmpHTML)

	outputPath := filepath.Join(r.outputDir, outputName)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "chromium",
		"--headless",
		"--no-sandbox",
		"--disable-gpu",
		"--disable-software-rasterizer",
		"--hide-scrollbars",
		"--window-size=1080,1920",
		"--default-background-color=00000000",
		"--screenshot="+outputPath,
		"file://"+tmpHTML,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("chromium screenshot failed: %w\nOutput: %s", err, string(output))
	}

	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return "", fmt.Errorf("screenshot file not created: %s", outputPath)
	}

	log.Printf("[template] ✅ Rendered %s", outputName)
	return outputPath, nil
}