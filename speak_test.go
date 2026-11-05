package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPiperSpeakerArgs(t *testing.T) {
	tests := []struct {
		name    string
		lang    string
		voice   string
		speaker string
		want    []string
	}{
		{"default LibriTTS speaker", "en", "", "", []string{"-s", "0"}},
		{"configured speaker", "en", "", "42", []string{"-s", "42"}},
		{"custom model without speaker", "en", "/tmp/custom.onnx", "", nil},
		{"Swahili uses its single voice", "sw", "", "", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PIPER_EN_VOICE", tt.voice)
			t.Setenv("PIPER_EN_SPEAKER", tt.speaker)
			if got := piperSpeakerArgs(tt.lang); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("piperSpeakerArgs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNormalizeMonthAbbreviations(t *testing.T) {
	tests := []struct {
		name string
		text string
		lang string
		want string
	}{
		{
			name: "English date with period",
			text: "The report was published on Nov. 5, 2026.",
			lang: "en",
			want: "The report was published on November 5, 2026.",
		},
		{
			name: "English date without period",
			text: "Results are due in Sept 2026.",
			lang: "en",
			want: "Results are due in September 2026.",
		},
		{
			name: "Swahili month",
			text: "Ripoti ilichapishwa Nov. 5.",
			lang: "sw",
			want: "Ripoti ilichapishwa Novemba 5.",
		},
		{
			name: "preserves sentence punctuation",
			text: "Results arrived in Jan.",
			lang: "en",
			want: "Results arrived in January.",
		},
		{
			name: "preserves all-caps acronym",
			text: "The APR changes next month.",
			lang: "en",
			want: "The APR changes next month.",
		},
		{
			name: "expands uppercase date",
			text: "The deadline is NOV 5.",
			lang: "en",
			want: "The deadline is November 5.",
		},
		{
			name: "leaves full month names unchanged",
			text: "The event begins in March.",
			lang: "en",
			want: "The event begins in March.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeMonthAbbreviations(tt.text, tt.lang); got != tt.want {
				t.Errorf("normalizeMonthAbbreviations() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDownloadVoiceAsset(t *testing.T) {
	body := []byte("voice model test data")
	sum := sha256.Sum256(body)
	checksum := fmt.Sprintf("%x", sum)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer server.Close()

	t.Run("installs verified asset", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "voice.onnx")
		if err := downloadVoiceAsset(context.Background(), server.Client(), server.URL, path, int64(len(body)), checksum); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(body) {
			t.Fatalf("installed file = %q, want %q", got, body)
		}
	})

	t.Run("rejects incorrect checksum", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "voice.onnx")
		err := downloadVoiceAsset(context.Background(), server.Client(), server.URL, path, int64(len(body)), "incorrect")
		if err == nil {
			t.Fatal("expected checksum error")
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("voice file should not be installed, stat error = %v", err)
		}
	})
}
