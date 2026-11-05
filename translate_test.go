package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTranslateUsesUserSettings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("request path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer saved-key" {
			t.Errorf("authorization = %q", got)
		}
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if request.Model != "saved-model" {
			t.Errorf("model = %q", request.Model)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Habari"}}]}`))
	}))
	defer server.Close()

	got, err := translate(context.Background(), "Hello", "Swahili", userSettings{
		OpenAIAPIKey:  "saved-key",
		OpenAIBaseURL: server.URL + "/v1/",
		OpenAIModel:   "saved-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "Habari" {
		t.Errorf("translation = %q, want %q", got, "Habari")
	}
}

func TestTranslateNeedsSettings(t *testing.T) {
	if got := translateNeeds(userSettings{}); got != "Needs API key (Settings)" {
		t.Fatalf("missing key message = %q", got)
	}
	if got := translateNeeds(userSettings{OpenAIAPIKey: "key"}); got != "Needs model (Settings)" {
		t.Fatalf("missing model message = %q", got)
	}
	if got := translateNeeds(userSettings{OpenAIAPIKey: "key", OpenAIModel: "model"}); got != "" {
		t.Fatalf("complete settings message = %q", got)
	}
}
