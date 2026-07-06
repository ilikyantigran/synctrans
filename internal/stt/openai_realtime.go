package stt

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	// realtimeURL is the realtime endpoint with the transcription intent.
	realtimeURL = "wss://api.openai.com/v1/realtime?intent=transcription"
	// realtimeModel is the streaming transcription model. Decided in M0.
	realtimeModel = "gpt-4o-transcribe"

	// Continuous-mode defaults (all overridable via env — see NewOpenAIRealtime).
	defaultChunkMs       = 0    // 0 = server-VAD mode (default): OpenAI finds clean boundaries
	defaultMinChunkMs    = 1500 // manual mode only: earliest a clean cut may happen
	defaultSilenceHoldMs = 700  // pause to end a segment (server_vad silence_duration / manual hold)
	defaultSilenceRMS    = 400  // PCM16 RMS below this counts as silence
	defaultOverlapMs     = 1500 // audio re-injected after a hard cut

	idleFlush = 600 * time.Millisecond // flush a trailing chunk when audio stops
)

// envInt returns the int value of env var name, or def if unset/unparseable.
func envInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// envBool reports whether env var name is a truthy value (1/true/yes).
func envBool(name string, def bool) bool {
	if v := os.Getenv(name); v != "" {
		return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
	}
	return def
}

// bytesForMs / msForBytes convert between PCM16-mono byte counts and milliseconds.
func bytesForMs(ms int) int { return ms * SampleRate * 2 / 1000 }
func msForBytes(b int) int  { return b * 1000 / (SampleRate * 2) }

type OpenAIRealtime struct {
	apiKey        string
	manual        bool    // RELAY_MANUAL: no auto-segmentation; cut only on Flush()
	chunkMs       int     // >0: continuous mode hard-cut ceiling; 0: server VAD
	minChunkMs    int     // don't take a clean cut before this much audio
	silenceHoldMs int     // trailing silence that counts as a sentence pause
	silenceRMS    float64 // silence threshold for clean cuts (manual continuous mode)
	vadThreshold  float64 // server_vad speech/silence threshold (0..1)
	overlapMs     int     // overlap re-injected after a hard cut
}

func NewOpenAIRealtime() (*OpenAIRealtime, error) {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY not set")
	}
	t := &OpenAIRealtime{
		apiKey:        key,
		manual:        envBool("RELAY_MANUAL", false),
		chunkMs:       envInt("RELAY_CHUNK_MS", defaultChunkMs),
		minChunkMs:    envInt("RELAY_MIN_CHUNK_MS", defaultMinChunkMs),
		silenceHoldMs: envInt("RELAY_SILENCE_HOLD_MS", defaultSilenceHoldMs),
		overlapMs:     envInt("RELAY_OVERLAP_MS", defaultOverlapMs),
		silenceRMS:    defaultSilenceRMS,
		vadThreshold:  0.5,
	}
	if v := os.Getenv("RELAY_SILENCE_RMS"); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			t.silenceRMS = n
		}
	}
	if v := os.Getenv("RELAY_VAD_THRESHOLD"); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			t.vadThreshold = n
		}
	}
	return t, nil
}

func (t *OpenAIRealtime) Open(ctx context.Context, lang string) (Session, error) {
	sctx, cancel := context.WithCancel(ctx)

	conn, _, err := websocket.Dial(sctx, realtimeURL, &websocket.DialOptions{
		HTTPHeader: map[string][]string{
			"Authorization": {"Bearer " + t.apiKey},
		},
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("dial realtime: %w", err)
	}
	conn.SetReadLimit(1 << 20) // headroom over coder's 32 KiB default

	s := &openAISession{
		conn:          conn,
		ctx:           sctx,
		cancel:        cancel,
		results:       make(chan Result, 32),
		manual:        t.manual,
		chunkMs:       t.chunkMs,
		minChunkMs:    t.minChunkMs,
		silenceHoldMs: t.silenceHoldMs,
		silenceRMS:    t.silenceRMS,
		vadThreshold:  t.vadThreshold,
		overlapBytes:  bytesForMs(t.overlapMs),
	}

	// Configure the session for transcription before any audio flows.
	if err := s.configure(lang); err != nil {
		conn.Close(websocket.StatusInternalError, "configure failed")
		cancel()
		return nil, err
	}
	log.Printf("stt: realtime session opened (src=%s, model=%s, manual=%t, chunkMs=%d, silenceHoldMs=%d, vadThreshold=%.2f, minChunkMs=%d, silenceRMS=%.0f, overlapMs=%d)",
		lang, realtimeModel, t.manual, t.chunkMs, t.silenceHoldMs, t.vadThreshold, t.minChunkMs, t.silenceRMS, t.overlapMs)

	go s.readPump()
	if s.continuous() {
		go s.idleCommitLoop() // flush trailing audio when the speaker stops
	}
	return s, nil
}

// openAISession is one live upstream transcription stream.
type openAISession struct {
	conn    *websocket.Conn
	ctx     context.Context
	cancel  context.CancelFunc
	results chan Result

	manual bool // no auto-segmentation; cut only on Flush()

	// Continuous-mode config (chunkMs > 0 && !manual).
	chunkMs       int     // hard-cut ceiling; also enables continuous mode
	minChunkMs    int     // don't take a clean cut before this much audio
	silenceHoldMs int     // trailing silence that counts as a sentence pause
	silenceRMS    float64 // RMS below this = silence (manual mode)
	vadThreshold  float64 // server_vad speech/silence threshold
	overlapBytes  int     // re-injected after a hard cut

	mu           sync.Mutex // serializes all sends + continuous-mode state
	window       []byte     // PCM accumulated since the last commit
	silenceRunMs int        // consecutive trailing silence in window
	lastAppend   time.Time  // when the last audio frame arrived
}

// ─── Outgoing events (relay → OpenAI) ─────────────────────────────────────

type turnDetection struct {
	Type              string  `json:"type"`
	Threshold         float64 `json:"threshold,omitempty"`
	PrefixPaddingMs   int     `json:"prefix_padding_ms,omitempty"`
	SilenceDurationMs int     `json:"silence_duration_ms,omitempty"`
}

type sessionUpdate struct {
	Type    string `json:"type"`
	Session struct {
		Type  string `json:"type"`
		Audio struct {
			Input struct {
				Format struct {
					Type string `json:"type"`
					Rate int    `json:"rate"`
				} `json:"format"`
				Transcription struct {
					Model    string `json:"model"`
					Language string `json:"language,omitempty"`
				} `json:"transcription"`
				TurnDetection *turnDetection `json:"turn_detection"`
			} `json:"input"`
		} `json:"audio"`
	} `json:"session"`
}

type audioAppend struct {
	Type  string `json:"type"`
	Audio string `json:"audio"` // base64-encoded PCM16
}

type bufferCommit struct {
	Type string `json:"type"`
}

// serverVAD reports whether OpenAI's automatic VAD owns the boundaries.
func (s *openAISession) serverVAD() bool { return !s.manual && s.chunkMs <= 0 }

// continuous reports whether we drive boundaries on a timer + RMS (manual chunking).
func (s *openAISession) continuous() bool { return !s.manual && s.chunkMs > 0 }

func (s *openAISession) configure(language string) error {
	var u sessionUpdate
	u.Type = "session.update"
	u.Session.Type = "transcription"
	u.Session.Audio.Input.Format.Type = "audio/pcm"
	u.Session.Audio.Input.Format.Rate = SampleRate
	u.Session.Audio.Input.Transcription.Model = realtimeModel
	u.Session.Audio.Input.Transcription.Language = language
	if s.serverVAD() {
		// Server-VAD mode: OpenAI finalizes on a trailing-silence pause.
		u.Session.Audio.Input.TurnDetection = &turnDetection{
			Type:              "server_vad",
			Threshold:         s.vadThreshold,
			PrefixPaddingMs:   300,
			SilenceDurationMs: s.silenceHoldMs,
		}
	}
	return s.send(u)
}

func (s *openAISession) Write(pcm []byte) error {
	if s.continuous() {
		return s.writeContinuous(pcm)
	}
	return s.send(audioAppend{
		Type:  "input_audio_buffer.append",
		Audio: base64.StdEncoding.EncodeToString(pcm),
	})
}

func (s *openAISession) Flush() error {
	if s.serverVAD() {
		return nil
	}
	return s.send(bufferCommit{Type: "input_audio_buffer.commit"})
}

func (s *openAISession) writeContinuous(pcm []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.writeRaw(audioAppend{
		Type:  "input_audio_buffer.append",
		Audio: base64.StdEncoding.EncodeToString(pcm),
	}); err != nil {
		return err
	}
	s.window = append(s.window, pcm...)
	s.lastAppend = time.Now()

	if rmsInt16(pcm) < s.silenceRMS {
		s.silenceRunMs += msForBytes(len(pcm))
	} else {
		s.silenceRunMs = 0
	}

	dur := msForBytes(len(s.window))
	switch {
	case dur < s.minChunkMs:
		return nil // too short to cut yet
	case s.silenceRunMs >= s.silenceHoldMs:
		return s.commitLocked(false) // clean cut at a sentence pause — no overlap
	case dur >= s.chunkMs:
		return s.commitLocked(true) // ran past the ceiling with no gap — hard cut
	default:
		return nil
	}
}

func (s *openAISession) idleCommitLoop() {
	t := time.NewTicker(150 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			s.mu.Lock()
			if len(s.window) >= bytesForMs(200) && time.Since(s.lastAppend) > idleFlush {
				_ = s.commitLocked(false)
			}
			s.mu.Unlock()
		}
	}
}

func (s *openAISession) commitLocked(overlap bool) error {
	if err := s.writeRaw(bufferCommit{Type: "input_audio_buffer.commit"}); err != nil {
		return err
	}
	s.silenceRunMs = 0

	if overlap && s.overlapBytes > 0 && len(s.window) > 0 {
		n := s.overlapBytes
		if n > len(s.window) {
			n = len(s.window)
		}
		tail := append([]byte(nil), s.window[len(s.window)-n:]...)
		if err := s.writeRaw(audioAppend{
			Type:  "input_audio_buffer.append",
			Audio: base64.StdEncoding.EncodeToString(tail),
		}); err != nil {
			return err
		}
		s.window = tail // next window starts with the overlap
		return nil
	}
	s.window = s.window[:0]
	return nil
}

// send locks the mutex and writes v. writeRaw assumes the mutex is already held.
func (s *openAISession) send(v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeRaw(v)
}

func (s *openAISession) writeRaw(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.conn.Write(s.ctx, websocket.MessageText, data)
}

// rmsInt16 is the root-mean-square amplitude of little-endian PCM16 samples.
func rmsInt16(pcm []byte) float64 {
	n := len(pcm) / 2
	if n == 0 {
		return 0
	}
	var sum float64
	for i := 0; i+1 < len(pcm); i += 2 {
		v := int16(binary.LittleEndian.Uint16(pcm[i : i+2]))
		sum += float64(v) * float64(v)
	}
	return math.Sqrt(sum / float64(n))
}

// ─── Incoming events (OpenAI → relay) ─────────────────────────────────────

// serverEvent captures just the fields we act on.
type serverEvent struct {
	Type       string `json:"type"`
	ItemID     string `json:"item_id"`
	Delta      string `json:"delta"`      // on …transcription.delta
	Transcript string `json:"transcript"` // on …transcription.completed
	Error      struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (s *openAISession) readPump() {
	defer close(s.results)
	for {
		_, data, err := s.conn.Read(s.ctx)
		if err != nil {
			return // ctx cancelled or upstream closed — end the stream
		}

		var ev serverEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			continue // unknown frame; skip
		}

		switch ev.Type {
		case "conversation.item.input_audio_transcription.delta":
			s.emit(Result{Final: false, Text: ev.Delta, ItemID: ev.ItemID})
		case "conversation.item.input_audio_transcription.completed":
			log.Printf("stt: final %q", ev.Transcript)
			s.emit(Result{Final: true, Text: ev.Transcript, ItemID: ev.ItemID})
		case "error":
			// Logged in full — this is where a bad schema or auth/quota surfaces.
			log.Printf("stt: upstream error: %s", string(data))
		default:
			// session.created/updated, speech_started/stopped, committed, …
			log.Printf("stt: event %s", ev.Type)
		}
	}
}

func (s *openAISession) emit(r Result) {
	select {
	case s.results <- r:
	case <-s.ctx.Done():
	}
}

func (s *openAISession) Results() <-chan Result { return s.results }

func (s *openAISession) Close() error {
	s.cancel()
	return s.conn.Close(websocket.StatusNormalClosure, "")
}
