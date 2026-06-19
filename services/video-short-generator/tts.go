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

// TTSGenerator generates audio + caption from text with emotional SSML
type TTSGenerator struct {
	outputDir    string
	voiceMale    string
	voiceFemale  string
}

func NewTTSGenerator(outputDir string) *TTSGenerator {
	ensureDir(outputDir)
	return &TTSGenerator{
		outputDir:   outputDir,
		voiceMale:   "id-ID-ArdiNeural",
		voiceFemale: "id-ID-GadisNeural",
	}
}

// VoiceCondition maps market condition to voice choice and emotion
type VoiceCondition struct {
	Voice    string // "male" or "female"
	Style    string // SSML mstts:express-as style
	Pitch    string // +5Hz, -3Hz, etc.
	Rate     string // +2%, -3%, etc.
}

// GetVoiceForCondition returns voice config based on market condition
func GetVoiceForCondition(condition int) VoiceCondition {
	switch condition {
	case 1: // ConditionBullishStrong — GOLDEN MOMENT / GOLDEN REVERSAL
		return VoiceCondition{Voice: "female", Style: "excited", Pitch: "+5Hz", Rate: "+5%"}
	case 2: // ConditionBullishModerate — BULLISH MOMENTUM / STRONG UPTREND
		return VoiceCondition{Voice: "female", Style: "cheerful", Pitch: "+3Hz", Rate: "+2%"}
	case 3: // ConditionBearishStreak — STRONG DOWNTREND / BEARISH MOMENTUM
		return VoiceCondition{Voice: "male", Style: "sad", Pitch: "-5Hz", Rate: "-3%"}
	case 4: // ConditionBearishLow — SELL PRESSURE / BEARISH REVERSAL
		return VoiceCondition{Voice: "male", Style: "serious", Pitch: "-3Hz", Rate: "-2%"}
	default:
		return VoiceCondition{Voice: "male", Style: "serious", Pitch: "+0Hz", Rate: "+0%"}
	}
}

// BuildSSML creates emotional SSML from script text
func BuildSSML(script string, vc VoiceCondition, fullVoiceName string) string {
	// Add pauses between sentences for natural flow
	script = regexp.MustCompile(`([.!?])\s+`).ReplaceAllString(script, "$1 <break time=\"0.4s\"/> ")

	ssml := fmt.Sprintf(`<speak version="1.0" xmlns="http://www.w3.org/2001/10/synthesis" xmlns:mstts="https://www.w3.org/2001/mstts" xml:lang="id-ID">
  <voice name="%s">
    <mstts:express-as style="%s" styledegree="1.5">
      <prosody pitch="%s" rate="%s">%s</prosody>
    </mstts:express-as>
  </voice>
</speak>`, fullVoiceName, vc.Style, vc.Pitch, vc.Rate, script)

	return ssml
}

// GenerateTTS generates emotional audio from script based on market condition
func (t *TTSGenerator) GenerateTTS(script string, filename string, condition int) (string, string, error) {
	audioPath := filepath.Join(t.outputDir, filename+".mp3")
	vttPath := filepath.Join(t.outputDir, filename+".vtt")
	assPath := filepath.Join(t.outputDir, filename+".ass")

	vc := GetVoiceForCondition(condition)
	voice := t.voiceMale
	if vc.Voice == "female" {
		voice = t.voiceFemale
	}

	ssml := BuildSSML(script, vc, voice)
	ssmlPath := filepath.Join(t.outputDir, filename+".ssml")
	if err := os.WriteFile(ssmlPath, []byte(ssml), 0644); err != nil {
		return "", "", fmt.Errorf("write SSML: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Use SSML file for emotional TTS
	cmd := exec.CommandContext(ctx, "edge-tts",
		"--voice", voice,
		"--file", ssmlPath,
		"--write-subtitles", vttPath,
		"--write-media", audioPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[tts] ⚠️ SSML failed, falling back to plain: %v", err)
		// Fallback: plain text with rate
		cmd = exec.CommandContext(ctx, "edge-tts",
			"--voice", voice,
			"--rate", vc.Rate,
			"--write-subtitles", vttPath,
			"--text", script,
			"--write-media", audioPath,
		)
		output, err = cmd.CombinedOutput()
		if err != nil {
			return "", "", fmt.Errorf("edge-tts failed: %w\nOutput: %s", err, string(output))
		}
	}

	// Cleanup SSML
	os.Remove(ssmlPath)

	if _, err := os.Stat(audioPath); os.IsNotExist(err) {
		return "", "", fmt.Errorf("audio file not created: %s", audioPath)
	}

	// Convert VTT to ASS
	if err := convertVTTtoASS(vttPath, assPath); err != nil {
		log.Printf("[tts] ⚠️ Caption conversion failed (non-blocking): %v", err)
		if err := generateBasicASS(assPath, audioPath); err != nil {
			return audioPath, "", err
		}
		return audioPath, assPath, nil
	}

	os.Remove(vttPath)
	log.Printf("[tts] ✅ Generated %s (voice=%s, style=%s) + %s", filepath.Base(audioPath), voice, vc.Style, filepath.Base(assPath))
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