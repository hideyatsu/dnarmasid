package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// TTSGenerator generates audio + caption from text
type TTSGenerator struct {
	outputDir string
	voice     string
}

func NewTTSGenerator(outputDir string) *TTSGenerator {
	ensureDir(outputDir)
	return &TTSGenerator{
		outputDir: outputDir,
		voice:     "id-ID-ArdiNeural",
	}
}

// GenerateTTS generates audio + caption from script
func (t *TTSGenerator) GenerateTTS(script string, filename string) (string, string, error) {
	audioPath := filepath.Join(t.outputDir, filename+".mp3")
	vttPath := filepath.Join(t.outputDir, filename+".vtt")
	assPath := filepath.Join(t.outputDir, filename+".ass")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "edge-tts",
		"--voice", t.voice,
		"--rate", "+5%",
		"--write-subtitles", vttPath,
		"--text", script,
		"--write-media", audioPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("edge-tts failed: %w\nOutput: %s", err, string(output))
	}

	if _, err := os.Stat(audioPath); os.IsNotExist(err) {
		return "", "", fmt.Errorf("audio file not created: %s", audioPath)
	}

	// Convert VTT to ASS
	if err := convertVTTtoASS(vttPath, assPath); err != nil {
		log.Printf("[tts] ⚠️ Caption conversion failed (non-blocking): %v", err)
		// Fallback: generate basic ASS without timing
		if err := generateBasicASS(assPath, audioPath); err != nil {
			return audioPath, "", err
		}
		return audioPath, assPath, nil
	}

	// Remove VTT (we only need ASS)
	os.Remove(vttPath)

	log.Printf("[tts] ✅ Generated %s + %s", filepath.Base(audioPath), filepath.Base(assPath))
	return audioPath, assPath, nil
}

func convertVTTtoASS(vttPath, assPath string) error {
	file, err := os.Open(vttPath)
	if err != nil {
		return fmt.Errorf("open vtt: %w", err)
	}
	defer file.Close()

	type cue struct {
		start, end, text string
	}
	var cues []cue

	scanner := bufio.NewScanner(file)
	timePattern := regexp.MustCompile(`(\d{2}:\d{2}:\d{2}\.\d{3})\s*-->\s*(\d{2}:\d{2}:\d{2}\.\d{3})`)

	var currentStart, currentEnd, currentText string
	inCue := false

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "WEBVTT") || line == "" {
			continue
		}

		if matches := timePattern.FindStringSubmatch(line); len(matches) == 3 {
			if currentStart != "" && currentText != "" {
				cues = append(cues, cue{currentStart, currentEnd, currentText})
			}
			currentStart = matches[1]
			currentEnd = matches[2]
			currentText = ""
			inCue = true
			continue
		}

		if inCue && line != "" {
			if currentText == "" {
				currentText = line
			} else {
				currentText += "\\N" + line
			}
		}
	}

	if currentStart != "" && currentText != "" {
		cues = append(cues, cue{currentStart, currentEnd, currentText})
	}

	if len(cues) == 0 {
		return fmt.Errorf("no cues found in VTT")
	}

	assFile, err := os.Create(assPath)
	if err != nil {
		return fmt.Errorf("create ass: %w", err)
	}
	defer assFile.Close()

	header := `[Script Info]
Title: DnarMasID Video Short Caption
ScriptType: v4.00+
PlayResX: 1080
PlayResY: 1920

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,Montserrat,48,&H00FFFFFF,&H000000FF,&H00000000,&H80000000,1,0,0,0,100,100,0,0,1,3,0,2,20,20,120,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
`
	assFile.WriteString(header)

	for _, c := range cues {
		start := formatASSTime(c.start)
		end := formatASSTime(c.end)
		assFile.WriteString(fmt.Sprintf("Dialogue: 0,%s,%s,Default,,0,0,0,,%s\n", start, end, c.text))
	}

	log.Printf("[tts] ✅ Converted VTT → ASS (%d cues)", len(cues))
	return nil
}

func generateBasicASS(assPath, audioPath string) error {
	// Get audio duration
	duration, err := GetAudioDuration(audioPath)
	if err != nil {
		duration = 18.0 // default
	}

	assFile, err := os.Create(assPath)
	if err != nil {
		return err
	}
	defer assFile.Close()

	header := `[Script Info]
Title: DnarMasID Video Short Caption
ScriptType: v4.00+
PlayResX: 1080
PlayResY: 1920

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,Montserrat,48,&H00FFFFFF,&H000000FF,&H00000000,&H80000000,1,0,0,0,100,100,0,0,1,3,0,2,20,20,120,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
`
	assFile.WriteString(header)
	endTime := fmt.Sprintf("%d:%02d:%05.2f", int(duration)/3600, (int(duration)%3600)/60, 0.0)
	_ = endTime
	assFile.WriteString("Dialogue: 0,0:00:00.00,0:00:18.00,Default,,0,0,0,,DnarMasID\n")
	return nil
}

func formatASSTime(vttTime string) string {
	parts := strings.Split(vttTime, ":")
	if len(parts) != 3 {
		return vttTime
	}
	hour := strings.TrimPrefix(parts[0], "0")
	if hour == "" {
		hour = "0"
	}
	msParts := strings.Split(parts[2], ".")
	ms := "00"
	if len(msParts) == 2 && len(msParts[1]) >= 2 {
		ms = msParts[1][:2]
	}
	return fmt.Sprintf("%s:%s:%s.%s", hour, parts[1], msParts[0], ms)
}

func GetAudioDuration(audioPath string) (float64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		audioPath,
	)

	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe failed: %w", err)
	}

	var duration float64
	fmt.Sscanf(strings.TrimSpace(string(output)), "%f", &duration)
	return duration, nil
}