package stt

import "context"

const SampleRate = 24000

// Result is one transcription event from the upstream STT.
type Result struct {
	Final bool
	// Text is the source-language transcription (Hebrew, in our case).
	Text   string
	ItemID string
}

type Session interface {
	Write(pcm []byte) error

	Flush() error

	Results() <-chan Result

	Close() error
}

type Transcriber interface {
	Open(ctx context.Context, lang string) (Session, error)
}
