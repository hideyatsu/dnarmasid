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
)

// VideoStitcher composes video from frames + audio using FFmpeg
type VideoStitcher struct {
	outputDir string
}

func NewVideoStitcher(outputDir string) *VideoStitcher {
	ensureDir(outputDir)
	return &VideoStitcher{outputDir: outputDir}
}

type StitchOptions struct {
	HookFrame   string
	PriceFrame  string
	CTAFrame    string
	AudioPath   string
	CaptionPath string
	BgVideo     string  // optional (Phase 2)
	Duration    float64
	OutputName  string
}

// Stitch creates the final video
func (s *VideoStitcher) Stitch(opts StitchOptions) (string, error) {
	outputPath := filepath.Join(s.outputDir, opts.OutputName)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var args []string
	if opts.BgVideo != "" {
		args = []string{
			"-y",
			"-stream_loop", "-1",
			"-i", opts.BgVideo,
			"-loop", "1", "-i", opts.HookFrame,
			"-loop", "1", "-i", opts.PriceFrame,
			"-loop", "1", "-i", opts.CTAFrame,
			"-i", opts.AudioPath,
			"-filter_complex", buildFilterComplexWithBg(opts),
			"-map", "[out]",
			"-map", "4:a",
			"-c:v", "libx264", "-preset", "medium", "-crf", "23", "-pix_fmt", "yuv420p",
			"-c:a", "aac", "-b:a", "128k",
			"-t", fmt.Sprintf("%.1f", opts.Duration),
			"-shortest",
			outputPath,
		}
	} else {
		args = []string{
			"-y",
			"-loop", "1", "-i", opts.HookFrame,
			"-loop", "1", "-i", opts.PriceFrame,
			"-loop", "1", "-i", opts.CTAFrame,
			"-i", opts.AudioPath,
			"-filter_complex", buildFilterComplexStatic(opts),
			"-map", "[out]",
			"-map", "3:a",
			"-c:v", "libx264", "-preset", "medium", "-crf", "23", "-pix_fmt", "yuv420p",
			"-c:a", "aac", "-b:a", "128k",
			"-t", fmt.Sprintf("%.1f", opts.Duration),
			"-shortest",
			outputPath,
		}
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ffmpeg stitch failed: %w\nOutput: %s", err, string(output))
	}

	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return "", fmt.Errorf("output video not created: %s", outputPath)
	}

	log.Printf("[stitch] ✅ Video created: %s", outputPath)
	return outputPath, nil
}

func buildFilterComplexStatic(opts StitchOptions) string {
	return fmt.Sprintf(
		`color=c=#1a1a1a:s=1080x1920:d=%.1f,format=yuv420p[bg];
[0:v]format=rgba[hook];
[1:v]format=rgba[price];
[2:v]format=rgba[cta];
[bg][hook]overlay=(W-w)/2:(H-h)/2:enable='between(t,0,4.5)'[bg1];
[bg1][price]overlay=(W-w)/2:(H-h)/2:enable='between(t,4,13.5)'[bg2];
[bg2][cta]overlay=(W-w)/2:(H-h)/2:enable='between(t,13,18.5)'[bg3];
[bg3]subtitles=%s[out]`,
		opts.Duration,
		escapeFFmpegPath(opts.CaptionPath),
	)
}

func buildFilterComplexWithBg(opts StitchOptions) string {
	return fmt.Sprintf(
		`[0:v]scale=1080:1920,setpts=PTS-STARTPTS,format=yuv420p[bg];
[1:v]format=rgba[hook];
[2:v]format=rgba[price];
[3:v]format=rgba[cta];
[bg][hook]overlay=(W-w)/2:(H-h)/2:enable='between(t,0,4.5)'[bg1];
[bg1][price]overlay=(W-w)/2:(H-h)/2:enable='between(t,4,13.5)'[bg2];
[bg2][cta]overlay=(W-w)/2:(H-h)/2:enable='between(t,13,18.5)'[bg3];
[bg3]subtitles=%s[out]`,
		escapeFFmpegPath(opts.CaptionPath),
	)
}

func escapeFFmpegPath(path string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`:`, `\:`,
		`'`, `\'`,
	)
	return replacer.Replace(path)
}