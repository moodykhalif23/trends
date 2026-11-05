package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const kokoroBytesPerSec = 24000 * 2

var billionAbbreviation = regexp.MustCompile(`(?i)\b(\d[\d,]*(?:\.\d+)?)\s*bn\b`)
var millionAbbreviation = regexp.MustCompile(`(?i)\b(\d[\d,]*(?:\.\d+)?)\s*m\b`)
var dollarAmount = regexp.MustCompile(`(?i)(US)?\$\s*(\d[\d,]*(?:\.\d+)?)\s*(billion|bn|b|million|mn|m|thousand|k|trillion|T)?\b`)
var trillionAbbreviation = regexp.MustCompile(`(?i)\b(\d[\d,]*(?:\.\d+)?)\s*T\b`)
var monthAbbreviation = regexp.MustCompile(`(?i)\b(Jan|Feb|Mar|Apr|Jun|Jul|Aug|Sept|Sep|Oct|Nov|Dec)\b\.?`)
var localNamePronunciations = []struct {
	pattern *regexp.Regexp
	spoken  string
}{
	{regexp.MustCompile(`(?i)\bKisii\b`), "kee-see"},
	{regexp.MustCompile(`(?i)\bKenya\b`), "ken-yah"},
	{regexp.MustCompile(`(?i)\bNairobi\b`), "nai-ro-bee"},
	{regexp.MustCompile(`(?i)\bMombasa\b`), "mom-bah-sah"},
	{regexp.MustCompile(`(?i)\bKisumu\b`), "kee-soo-moo"},
	{regexp.MustCompile(`(?i)\bNakuru\b`), "nah-koo-roo"},
	{regexp.MustCompile(`(?i)\bEldoret\b`), "el-do-ret"},
	{regexp.MustCompile(`(?i)\bGarissa\b`), "gah-riss-ah"},
	{regexp.MustCompile(`(?i)\bKakamega\b`), "kah-kah-meh-gah"},
	{regexp.MustCompile(`(?i)\bMachakos\b`), "mah-chah-kos"},
	{regexp.MustCompile(`(?i)\bNaivasha\b`), "nai-vah-shah"},
	{regexp.MustCompile(`(?i)\bKajiado\b`), "kah-jee-ah-doh"},
	{regexp.MustCompile(`(?i)\bTurkana\b`), "tur-kah-nah"},
	{regexp.MustCompile(`(?i)\bSamburu\b`), "sahm-boo-roo"},
	{regexp.MustCompile(`(?i)\bKalenjin\b`), "kah-len-jin"},
	{regexp.MustCompile(`(?i)\bKikuyu\b`), "kee-koo-yoo"},
	{regexp.MustCompile(`(?i)\bLuhya\b`), "loo-hyah"},
	{regexp.MustCompile(`(?i)\bLuo\b`), "loo-oh"},
	{regexp.MustCompile(`(?i)\bMaasai\b`), "mah-sigh"},
	{regexp.MustCompile(`(?i)\bNyeri\b`), "nyeh-ree"},
	{regexp.MustCompile(`(?i)\bKiambu\b`), "kee-ahm-boo"},
	{regexp.MustCompile(`(?i)\bMeru\b`), "meh-roo"},
	{regexp.MustCompile(`(?i)\bEmbu\b`), "em-boo"},
	{regexp.MustCompile(`(?i)\bLamu\b`), "lah-moo"},
}

var months = map[string][2]string{
	"jan":  {"January", "Januari"},
	"feb":  {"February", "Februari"},
	"mar":  {"March", "Machi"},
	"apr":  {"April", "Aprili"},
	"jun":  {"June", "Juni"},
	"jul":  {"July", "Julai"},
	"aug":  {"August", "Agosti"},
	"sep":  {"September", "Septemba"},
	"sept": {"September", "Septemba"},
	"oct":  {"October", "Oktoba"},
	"nov":  {"November", "Novemba"},
	"dec":  {"December", "Desemba"},
}

// speakNeeds reports what is missing to speak in lang, or "".
func speakNeeds() string {
	python := strings.TrimSpace(os.Getenv("PYTHON"))
	if python == "" {
		python = "python3"
	}
	if _, err := exec.LookPath(python); err != nil {
		return "Needs Python 3.10–3.13"
	}
	if m := missing("aplay"); m != "" {
		return "Needs " + m
	}
	return ""
}

type speaker struct {
	lang   string
	voice  string
	lines  []string
	pcm    [][]byte
	engine *kokoroRuntime
}

func (s *speaker) prefetchInitial(ctx context.Context, count int) error {
	for len(s.pcm) < min(count, len(s.lines)) {
		pcm, err := synth(ctx, s.lines[len(s.pcm)], s.lang, s.voice, s.engine, nil)
		if err != nil {
			return err
		}
		s.pcm = append(s.pcm, pcm)
	}
	return nil
}

func (s *speaker) play(ctx context.Context, from int) (int, error) {
	ctx, stop := context.WithCancel(ctx)
	defer stop()

	if s.engine == nil {
		return 0, fmt.Errorf("Kokoro runtime is not configured")
	}
	play := exec.CommandContext(ctx, "aplay", "-q", "-r", "24000", "-f", "S16_LE", "-c", "1")
	in, err := play.StdinPipe()
	if err != nil {
		return 0, err
	}
	if err := play.Start(); err != nil {
		return 0, err
	}

	// Synthesize one line ahead of playback, skipping lines an earlier run already made.
	audio := make(chan []byte, 2)
	var synthErr error
	var wg sync.WaitGroup
	wg.Go(func() {
		defer close(audio)
		for i := range s.lines {
			if i >= len(s.pcm) {
				pcm, err := synth(ctx, s.lines[i], s.lang, s.voice, s.engine, func(chunk []byte) error {
					select {
					case audio <- chunk:
					case <-ctx.Done():
					}
					return nil
				})
				if err != nil {
					synthErr = err
					return
				}
				s.pcm = append(s.pcm, pcm)
				if ctx.Err() != nil {
					return
				}
				continue
			}
			select {
			case audio <- s.pcm[i]:
			case <-ctx.Done():
				return
			}
		}
	})

	var start time.Time
	written := 0
	var writeErr error
	for pcm := range audio {
		if from >= len(pcm) {
			from -= len(pcm)
			continue
		}
		if start.IsZero() {
			start = time.Now()
		}
		n, err := in.Write(pcm[from:])
		from = 0
		written += n
		if err != nil {
			writeErr = err
			stop()
			break
		}
	}
	wg.Wait()
	closeErr := in.Close()
	err = errors.Join(synthErr, writeErr, closeErr, play.Wait())
	if start.IsZero() {
		return 0, err
	}
	heard := min(written, int(time.Since(start).Seconds()*kokoroBytesPerSec))
	return max(heard-kokoroBytesPerSec/2, 0) &^ 1, err
}

func synth(ctx context.Context, line, lang, voice string, engine *kokoroRuntime, emit func([]byte) error) ([]byte, error) {
	text, heading := strings.CutPrefix(line, "## ")
	text = normalizeSpeechText(text, lang)
	if heading && text != "" && !strings.ContainsAny(text[len(text)-1:], ".!?") {
		text += "."
	}
	var pcm []byte
	appendChunk := func(chunk []byte) error {
		pcm = append(pcm, chunk...)
		if emit != nil {
			return emit(chunk)
		}
		return nil
	}
	if err := engine.synthesizeChunks(ctx, voice, text, appendChunk); err != nil {
		return nil, err
	}
	if heading {
		if err := appendChunk(make([]byte, kokoroBytesPerSec/10)); err != nil {
			return nil, err
		}
	}
	return pcm, nil
}

func normalizeSpeechText(text, lang string) string {
	trillion, billion, million := "trillion", "billion", "million"
	thousand, dollars := "thousand", "US dollars"
	if lang == "sw" {
		trillion, billion, million = "trilioni", "bilioni", "milioni"
		thousand, dollars = "elfu", "dola za Marekani"
	}
	text = dollarAmount.ReplaceAllStringFunc(text, func(amount string) string {
		parts := dollarAmount.FindStringSubmatch(amount)
		scale := strings.ToLower(parts[3])
		switch scale {
		case "t", "trillion":
			scale = trillion
		case "b", "bn", "billion":
			scale = billion
		case "m", "mn", "million":
			scale = million
		case "k", "thousand":
			scale = thousand
		default:
			scale = ""
		}
		currency := dollars
		if lang == "en" && scale == "" {
			value, err := strconv.ParseFloat(strings.ReplaceAll(parts[2], ",", ""), 64)
			if err == nil && value == 1 {
				currency = "US dollar"
			}
		}
		return strings.Join(strings.Fields(parts[2]+" "+scale+" "+currency), " ")
	})
	text = trillionAbbreviation.ReplaceAllString(text, "${1} "+trillion)
	text = billionAbbreviation.ReplaceAllString(text, "${1} "+billion)
	text = millionAbbreviation.ReplaceAllString(text, "${1} "+million)
	text = normalizeMonthAbbreviations(text, lang)
	return normalizeLocalPronunciations(text)
}

func normalizeLocalPronunciations(text string) string {
	for _, pronunciation := range localNamePronunciations {
		text = pronunciation.pattern.ReplaceAllStringFunc(text, func(name string) string {
			if name[0] >= 'A' && name[0] <= 'Z' {
				return strings.ToUpper(pronunciation.spoken[:1]) + pronunciation.spoken[1:]
			}
			return pronunciation.spoken
		})
	}
	return text
}

func splitSpeechLines(lines []string) []string {
	var segments []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			segments = append(segments, line)
		}
	}
	return segments
}

func prepareSpeechSegments(lines []string) []string {
	segments := splitSpeechLines(lines)
	if len(segments) < 2 || !strings.HasPrefix(segments[0], "## ") {
		return segments
	}

	var opening []string
	for len(segments) > 0 && strings.HasPrefix(segments[0], "## ") {
		opening = append(opening, strings.TrimSpace(strings.TrimPrefix(segments[0], "## ")))
		segments = segments[1:]
	}
	if len(segments) > 0 {
		opening = append(opening, segments[0])
		segments = segments[1:]
	}
	if len(opening) == 0 {
		return segments
	}
	for i := range opening {
		opening[i] = strings.TrimRight(opening[i], " \t\r\n.!?;:")
	}
	return append([]string{"## " + strings.Join(opening, ". ") + "."}, segments...)
}

func normalizeMonthAbbreviations(text, lang string) string {
	matches := monthAbbreviation.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return text
	}

	var result strings.Builder
	last := 0
	for _, match := range matches {
		abbreviation := text[match[0]:match[1]]
		abbreviatedMonth := strings.TrimSuffix(abbreviation, ".")
		after := strings.TrimLeft(text[match[1]:], " \t\r\n")
		dateFollows := len(after) > 0 && after[0] >= '0' && after[0] <= '9'
		if abbreviatedMonth == strings.ToUpper(abbreviatedMonth) && !strings.HasSuffix(abbreviation, ".") && !dateFollows {
			result.WriteString(text[last:match[1]])
			last = match[1]
			continue
		}

		key := strings.ToLower(abbreviatedMonth)
		name := months[key][0]
		if lang == "sw" {
			name = months[key][1]
		}
		result.WriteString(text[last:match[0]])
		result.WriteString(name)
		if strings.HasSuffix(abbreviation, ".") && !dateFollows {
			result.WriteByte('.')
		}
		last = match[1]
	}
	result.WriteString(text[last:])
	return result.String()
}
