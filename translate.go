package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

func translateNeeds() string {
	if os.Getenv("OPENAI_API_KEY") == "" {
		return "Needs OPENAI_API_KEY"
	}
	if os.Getenv("OPENAI_MODEL") == "" {
		return "Needs OPENAI_MODEL"
	}
	return ""
}

// translate calls an OpenAI-compatible chat endpoint (OpenRouter, Groq, ...) set up in .env.
func translate(ctx context.Context, text, language string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model": os.Getenv("OPENAI_MODEL"),
		"messages": []map[string]string{
			{"role": "system", "content": "Translate the user's text into " + language + ". Keep one paragraph per line. Reply with the translation only."},
			{"role": "user", "content": text},
		},
	})
	base := cmp.Or(os.Getenv("OPENAI_BASE_URL"), "https://openrouter.ai/api/v1")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv("OPENAI_API_KEY"))
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
