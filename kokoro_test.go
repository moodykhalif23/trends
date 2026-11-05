package main

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestKokoroFloat32ToPCM16(t *testing.T) {
	samples := []float32{-1, -0.5, 0, 0.5, 1, 2}
	input := make([]byte, len(samples)*4)
	for i, sample := range samples {
		binary.LittleEndian.PutUint32(input[i*4:], math.Float32bits(sample))
	}

	got, err := kokoroFloat32ToPCM16(input)
	if err != nil {
		t.Fatal(err)
	}
	want := []int16{-32767, -16384, 0, 16384, 32767, 32767}
	if len(got) != len(want)*2 {
		t.Fatalf("got %d bytes, want %d", len(got), len(want)*2)
	}
	for i, value := range want {
		if sample := int16(binary.LittleEndian.Uint16(got[i*2:])); sample != value {
			t.Errorf("sample %d = %d, want %d", i, sample, value)
		}
	}
}

func TestKokoroFloat32ToPCM16RejectsInvalidInput(t *testing.T) {
	if _, err := kokoroFloat32ToPCM16([]byte{1, 2, 3}); err == nil {
		t.Fatal("expected an alignment error")
	}

	input := make([]byte, 4)
	binary.LittleEndian.PutUint32(input, math.Float32bits(float32(math.NaN())))
	if _, err := kokoroFloat32ToPCM16(input); err == nil {
		t.Fatal("expected a non-finite sample error")
	}
}

func TestKokoroVoiceValidation(t *testing.T) {
	for _, voice := range []string{"af_heart", "af_bella", "am_adam", "am_santa"} {
		if !isKokoroVoice(voice) {
			t.Errorf("expected %q to be supported", voice)
		}
	}
	if isKokoroVoice("unknown") {
		t.Fatal("unknown voice should not be supported")
	}
}

func TestSettingsSaveClearsEnvironmentSeededKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "environment-key")
	t.Setenv("OPENAI_MODEL", "environment-model")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("KOKORO_VOICE", "")

	settings, err := openSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got := settings.snapshot().OpenAIAPIKey; got != "environment-key" {
		t.Fatalf("initial API key = %q, want environment value", got)
	}
	if err := settings.save(userSettings{
		OpenAIBaseURL: defaultOpenAIBaseURL,
		KokoroVoice:   "af_heart",
	}); err != nil {
		t.Fatal(err)
	}

	reopened, err := openSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.snapshot(); got.OpenAIAPIKey != "" || got.OpenAIModel != "" {
		t.Fatalf("cleared settings reloaded as %+v", got)
	}
	info, err := os.Stat(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "trends", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("settings permissions = %o, want 600", info.Mode().Perm())
	}
}
