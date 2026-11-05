package main

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestNormalizeLocalPronunciations(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "Kisii County is in Kenya.", want: "Kee-see County is in Ken-yah."},
		{input: "MOMBASA and kisii", want: "Mom-bah-sah and kee-see"},
		{input: "Kisumu, Nakuru, and Kikuyu", want: "Kee-soo-moo, Nah-koo-roo, and Kee-koo-yoo"},
	}
	for _, tt := range tests {
		if got := normalizeLocalPronunciations(tt.input); got != tt.want {
			t.Errorf("normalizeLocalPronunciations(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSplitSpeechLinesKeepsParagraphBoundaries(t *testing.T) {
	got := splitSpeechLines([]string{"## Kisii County. Local news?", "Habari za asubuhi. Kenya.", "Dr. Amina reported 3.5 cases. More news."})
	want := []string{"## Kisii County. Local news?", "Habari za asubuhi. Kenya.", "Dr. Amina reported 3.5 cases. More news."}
	if len(got) != len(want) {
		t.Fatalf("got %d speech paragraphs, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("segment %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestPrepareSpeechSegmentsCombinesOpeningHeadingAndParagraph(t *testing.T) {
	got := prepareSpeechSegments([]string{
		"## Kisii County News",
		"## County updates",
		"Kisii residents welcomed the new service. Work began this week.",
		"More details will follow.",
	})
	want := []string{
		"## Kisii County News. County updates. Kisii residents welcomed the new service. Work began this week.",
		"More details will follow.",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d segments, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("segment %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestKokoroRuntimeSynthesizesSpeech(t *testing.T) {
	if os.Getenv("TRENDS_KOKORO_INTEGRATION") != "1" {
		t.Skip("set TRENDS_KOKORO_INTEGRATION=1 to run Kokoro model integration")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	runtime := newKokoroRuntime()
	defer func() {
		if err := runtime.close(); err != nil {
			t.Errorf("close Kokoro runtime: %v", err)
		}
	}()

	started := time.Now()
	if err := runtime.prepare(ctx, "af_heart"); err != nil {
		t.Fatal(err)
	}
	t.Logf("Kokoro engine preparation and warm-up: %s", time.Since(started))
	for _, text := range []string{
		"Kisii County is in Kenya. Mombasa is a coastal city. Nairobi is the capital.",
		"Habari za asubuhi. Kisii ni nyumbani kwa watu wengi. Kenya ina miji mingi.",
	} {
		started = time.Now()
		firstChunk := time.Duration(0)
		chunks, audioBytes := 0, 0
		for _, sentence := range splitSpeechLines([]string{text}) {
			err := runtime.synthesizeChunks(ctx, "af_heart", sentence, func(chunk []byte) error {
				if chunks == 0 {
					firstChunk = time.Since(started)
				}
				chunks++
				audioBytes += len(chunk)
				return nil
			})
			if err != nil {
				t.Fatalf("synthesize %q: %v", sentence, err)
			}
		}
		if chunks == 0 || audioBytes == 0 || audioBytes%2 != 0 {
			t.Errorf("audio for %q has %d chunks and %d bytes", text, chunks, audioBytes)
		}
		t.Logf("Kokoro generated %d chunks for %q; first chunk in %s, total in %s (%d PCM bytes)", chunks, text, firstChunk, time.Since(started), audioBytes)
	}
}
