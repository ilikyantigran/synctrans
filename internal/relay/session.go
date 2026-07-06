package relay

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	"github.com/coder/websocket"

	"synctrans/internal/protocol"
	"synctrans/internal/stt"
	"synctrans/internal/translate"
)

type session struct {
	conn *websocket.Conn
	stt  stt.Session
	tr   *translate.Translator

	srcLang string // spoken language, reported on interim/segment (ISO-639-1)
	tgtLang string // translation target, used by translateLoop (ISO-639-1)

	out chan protocol.ServerMessage // all outbound messages funnel here
	seg uint64                      // last assigned SegID; touched only by sttPump
}

type finalSeg struct {
	id   uint64
	text string
}

func (s *session) run(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Audio can be bursty; give the phone headroom above coder's 32 KiB default.
	s.conn.SetReadLimit(1 << 20)

	finals := make(chan finalSeg, 16)
	writerDone := make(chan struct{})
	translateDone := make(chan struct{})

	go func() { defer close(writerDone); s.writeLoop(ctx) }()
	go func() { defer close(translateDone); s.translateLoop(ctx, finals) }()
	go s.sttPump(finals) // closes finals when STT Results drains

	// Announce readiness, then block on the phone until it hangs up or stops.
	log.Printf("session: ready (%s->%s)", s.srcLang, s.tgtLang)
	s.out <- protocol.ServerMessage{Type: protocol.ServerReady}
	s.readLoop(ctx)
	log.Printf("session: ended (%s->%s)", s.srcLang, s.tgtLang)

	// Phone is done → unwind the pipeline.
	cancel()          // cancel any in-flight Claude translate
	_ = s.stt.Close() // → Results closes → sttPump → close(finals)
	<-translateDone   // translateLoop (last out producer) has finished
	close(s.out)      // safe now: no goroutine writes to out anymore
	<-writerDone
}

// readLoop pumps phone → STT. Runs in run's goroutine.
func (s *session) readLoop(ctx context.Context) {
	var frames int
	for {
		typ, data, err := s.conn.Read(ctx)
		if err != nil {
			log.Printf("session: read ended after %d audio frames: %v", frames, err)
			return // phone disconnected or ctx cancelled
		}
		switch typ {
		case websocket.MessageBinary:
			frames++
			if frames == 1 || frames%100 == 0 {
				log.Printf("session: received audio frame #%d (%d bytes)", frames, len(data))
			}
			if err := s.stt.Write(data); err != nil {
				log.Printf("stt write: %v", err)
				return
			}
		case websocket.MessageText:
			var m protocol.ClientMessage
			if err := json.Unmarshal(data, &m); err != nil {
				continue
			}
			switch m.Type {
			case protocol.ClientStop:
				return // tear down; server VAD will have flushed the last phrase
			case protocol.ClientCut:
				// User tapped "cut": finalize the current fragment now.
				if err := s.stt.Flush(); err != nil {
					log.Printf("flush: %v", err)
				}
			}
			// ClientStart / ClientConfig (e.g. target language): handled in M4.
		}
	}
}

func (s *session) sttPump(finals chan<- finalSeg) {
	defer close(finals)

	var interim strings.Builder // running text for the current utterance
	var curItem string          // STT item id the interim belongs to

	for r := range s.stt.Results() {
		if r.Final {
			interim.Reset()
			curItem = ""
			s.seg++
			id := s.seg
			s.out <- protocol.ServerMessage{
				Type: protocol.ServerSegment, SegID: id, Text: r.Text, Lang: s.srcLang,
			}
			finals <- finalSeg{id: id, text: r.Text}
			continue
		}
		if r.ItemID != curItem {
			interim.Reset()
			curItem = r.ItemID
		}
		interim.WriteString(r.Text)
		if interim.Len() > 0 {
			s.out <- protocol.ServerMessage{
				Type: protocol.ServerInterim, Text: interim.String(), Lang: s.srcLang,
			}
		}
	}
}

const maxContext = 10

func (s *session) translateLoop(ctx context.Context, finals <-chan finalSeg) {
	var history []translate.Pair
	for f := range finals {
		log.Printf("translate: seg %d %q -> %s", f.id, f.text, s.tgtLang)
		translated, err := s.tr.Translate(ctx, f.text, s.tgtLang, history)
		if err != nil {
			if ctx.Err() != nil {
				return // shutting down; stop draining
			}
			log.Printf("translate seg %d: %v", f.id, err)
			s.out <- protocol.ServerMessage{
				Type: protocol.ServerError, Message: "translation failed",
			}
			continue
		}
		s.out <- protocol.ServerMessage{
			Type: protocol.ServerTranslation, SegID: f.id, Text: translated, Lang: s.tgtLang,
		}

		// Append to the rolling context, keeping only the last maxContext pairs.
		history = append(history, translate.Pair{Source: f.text, Target: translated})
		if len(history) > maxContext {
			history = history[len(history)-maxContext:]
		}
	}
}

func (s *session) writeLoop(ctx context.Context) {
	for m := range s.out {
		data, err := json.Marshal(m)
		if err != nil {
			continue
		}
		if err := s.conn.Write(ctx, websocket.MessageText, data); err != nil {
			for range s.out { // drain so producers don't block on a full channel
			}
			return
		}
	}
}
