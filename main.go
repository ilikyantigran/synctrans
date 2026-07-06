package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"synctrans/internal/stt"
	"synctrans/internal/translate"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatalf("usage: %s <hebrew-audio-file>", os.Args[0])
	}
	ctx := context.Background()

	fmt.Println("→ transcribing with OpenAI…")
	hebrew, err := stt.TranscribeFile(ctx, os.Args[1], "he")
	if err != nil {
		log.Fatalf("STT failed: %v", err)
	}
	fmt.Printf("Hebrew:  %s\n\n", hebrew)

	fmt.Println("→ translating with Claude Haiku…")
	english, err := translate.New().Translate(ctx, hebrew, "en", nil)
	if err != nil {
		log.Fatalf("translate failed: %v", err)
	}
	fmt.Printf("English: %s\n", english)
}
