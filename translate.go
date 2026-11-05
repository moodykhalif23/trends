package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func translateNeeds(settings userSettings) string {
	if strings.TrimSpace(settings.OpenAIAPIKey) == "" {
		return "Needs API key (Settings)"
	}
	if strings.TrimSpace(settings.OpenAIModel) == "" {
		return "Needs model (Settings)"
	}
	return ""
}

// translate calls an OpenAI-compatible chat endpoint configured in Settings.
func translate(ctx context.Context, text, language string, settings userSettings) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model": settings.OpenAIModel,
		"messages": []map[string]string{
			{"role": "system", "content": "Translate the user's text into " + language + ". Keep one paragraph per line and keep any leading '## ' markers. Reply with the translation only."},
			{"role": "user", "content": text},
		},
	})
	base := strings.TrimRight(strings.TrimSpace(settings.OpenAIBaseURL), "/")
	if base == "" {
		base = defaultOpenAIBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+settings.OpenAIAPIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("%s: %s", resp.Status, msg)
	}

	var res struct {
		Choices []struct{ Message struct{ Content string } }
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}
	if len(res.Choices) == 0 {
		return "", fmt.Errorf("empty reply from %s", base)
	}
	return res.Choices[0].Message.Content, nil
}
