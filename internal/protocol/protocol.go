package protocol

const SampleRate = 24000

// ─── Phone → relay (TEXT frames) ──────────────────────────────────────────

// ClientType enumerates control messages the phone may send.
type ClientType string

const (
	ClientStart ClientType = "start"
	// ClientStop signals the talk is over; the relay flushes the final segment.
	ClientStop ClientType = "stop"
	ClientCut  ClientType = "cut"
	// ClientConfig adjusts session settings mid-stream (e.g. target language).
	ClientConfig ClientType = "config"
)

type ClientMessage struct {
	Type       ClientType `json:"type"`
	TargetLang string     `json:"targetLang,omitempty"`
}

// ─── Relay → phone (TEXT frames) ──────────────────────────────────────────

// ServerType enumerates messages the relay sends down to the phone.
type ServerType string

const (
	ServerReady       ServerType = "ready"
	ServerInterim     ServerType = "interim"
	ServerSegment     ServerType = "segment"
	ServerTranslation ServerType = "translation"
	// ServerError reports a recoverable problem; the connection may stay open.
	ServerError ServerType = "error"
)

type ServerMessage struct {
	Type  ServerType `json:"type"`
	SegID uint64     `json:"segId,omitempty"`
	// Text is the transcription (interim/segment) or translation (translation).
	Text string `json:"text,omitempty"`
	// Lang is the ISO-639-1 code of Text ("he" for source, "en" for translation).
	Lang string `json:"lang,omitempty"`
	// Message carries human-readable detail for error.
	Message string `json:"message,omitempty"`
}
