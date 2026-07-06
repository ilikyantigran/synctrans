package relay

import (
	"crypto/subtle"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/coder/websocket"

	"synctrans/internal/protocol"
	"synctrans/internal/stt"
	"synctrans/internal/translate"
)

type Server struct {
	stt   stt.Transcriber
	tr    *translate.Translator
	token string // RELAY_AUTH_TOKEN; empty = no auth (local dev only)
}

func New(t stt.Transcriber, tr *translate.Translator) *Server {
	token := os.Getenv("RELAY_AUTH_TOKEN")
	if token == "" {
		log.Println("WARNING: RELAY_AUTH_TOKEN not set — relay is OPEN (fine for localhost, NOT for public deploy)")
	}
	return &Server{stt: t, tr: tr, token: token}
}

func (s *Server) authorized(r *http.Request) bool {
	if s.token == "" {
		return true // auth disabled
	}
	got := r.URL.Query().Get("token")
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		got = strings.TrimPrefix(h, "Bearer ")
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.handleWS)
}

// queryDefault returns the named query param, or def if absent/empty.
func queryDefault(r *http.Request, name, def string) string {
	if v := r.URL.Query().Get(name); v != "" {
		return v
	}
	return def
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		log.Printf("rejected unauthorized connection from %s", r.RemoteAddr)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Printf("ws accept: %v", err)
		return
	}
	defer conn.CloseNow()

	src := queryDefault(r, "src", "he")
	tgt := queryDefault(r, "tgt", "en")
	log.Printf("phone connected: %s->%s from %s", src, tgt, r.RemoteAddr)

	ctx := r.Context()
	sttSess, err := s.stt.Open(ctx, src)
	if err != nil {
		log.Printf("stt open: %v", err)
		conn.Close(websocket.StatusInternalError, "stt unavailable")
		return
	}

	sess := &session{
		conn:    conn,
		stt:     sttSess,
		tr:      s.tr,
		srcLang: src,
		tgtLang: tgt,
		out:     make(chan protocol.ServerMessage, 32),
	}
	sess.run(ctx)
}
