package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

func translateNeeds() string {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		return "Needs ANTHROPIC_API_KEY"
	}
	return ""
}

// translate asks Claude for the text in another language, keeping one paragraph per line.
func translate(ctx context.Context, text, language string) (string, error) {
	client := anthropic.NewClient()
	resp, err := client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
		Model:        "claude-opus-5-5",
		MaxTokens:    16000,
		OutputConfig: anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffortLow},
		Betas:        []anthropic.AnthropicBeta{"server-side-fallback-2026-07-01"},
		Fallbacks:    anthropic.BetaFallbacksParamOfDefault(),
		System: []anthropic.BetaTextBlockParam{{
			Text: "Translate the user's text into " + language + ". Keep one paragraph per line. Reply with the translation only.",
		}},
		Messages: []anthropic.BetaMessageParam{
			anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(text)),
		},
	})
	if err != nil {
		return "", err
	}
	if resp.StopReason == anthropic.BetaStopReasonRefusal {
		return "", fmt.Errorf("translation refused: %s", resp.StopDetails.Explanation)
	}
	var out strings.Builder
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			out.WriteString(t.Text)
		}
	}
	return out.String(), nil
}
