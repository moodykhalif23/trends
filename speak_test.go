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

	if err := runtime.prepare(ctx, "af_heart"); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Kisii County, Kenya.", "Habari za asubuhi."} {
		audio, err := runtime.synthesize(ctx, "af_heart", text)
		if err != nil {
			t.Fatalf("synthesize %q: %v", text, err)
		}
		if len(audio) == 0 || len(audio)%2 != 0 {
			t.Errorf("audio for %q has invalid length %d", text, len(audio))
		}
	}
}
