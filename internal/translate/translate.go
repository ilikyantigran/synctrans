package translate

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/anthropics/anthropic-sdk-go"
)

const defaultModel = anthropic.ModelClaudeHaiku4_5_20251001

func systemPrompt(targetLang string) string {
	return "You are a simultaneous interpreter translating consecutive segments of " +
		"one continuous live talk into natural, fluent " + langName(targetLang) + ". " +
		"The earlier messages are previous segments and your translations of them — " +
		"use them for context, flow, and consistent terminology. Translate ONLY the " +
		"user's latest message. If it is a sentence fragment, render it as a natural " +
		"continuation rather than a standalone phrase. The latest segment may begin " +
		"by repeating the last few words of the previous one (the audio windows " +
		"overlap) — do NOT translate that repeated part again; translate only the new " +
		"content, using the repeat only to resolve a word that was cut off. Output " +
		"ONLY the translation — no preamble, no quotes, no notes, no transliteration."
}

type Pair struct {
	Source string
	Target string
}

func langName(code string) string {
	if n, ok := languageNames[code]; ok {
		return n
	}
	return code
}

var languageNames = map[string]string{
	"en": "English",
	"he": "Hebrew",
	"ru": "Russian",
	"ar": "Arabic",
	"es": "Spanish",
	"fr": "French",
	"de": "German",
	"uk": "Ukrainian",
}

type Translator struct {
	client anthropic.Client
	model  anthropic.Model
}

func New() *Translator {
	model := defaultModel
	if v := os.Getenv("RELAY_TRANSLATE_MODEL"); v != "" {
		model = anthropic.Model(v)
	}
	log.Printf("translate: model=%s", model)
	return &Translator{client: anthropic.NewClient(), model: model}
}

func (t *Translator) Translate(ctx context.Context, text, targetLang string, history []Pair) (string, error) {
	msgs := make([]anthropic.MessageParam, 0, len(history)*2+1)
	for _, p := range history {
		msgs = append(msgs, anthropic.NewUserMessage(anthropic.NewTextBlock(p.Source)))
		msgs = append(msgs, anthropic.NewAssistantMessage(anthropic.NewTextBlock(p.Target)))
	}
	msgs = append(msgs, anthropic.NewUserMessage(anthropic.NewTextBlock(text)))

	resp, err := t.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     t.model,
		MaxTokens: 1024,
		System: []anthropic.TextBlockParam{{
			Text:         systemPrompt(targetLang),
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		}},
		Messages: msgs,
	})
	if err != nil {
		return "", fmt.Errorf("translate: %w", err)
	}

	out := ""
	for _, block := range resp.Content {
		if tb, ok := block.AsAny().(anthropic.TextBlock); ok {
			out += tb.Text
		}
	}
	return out, nil
}
