package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"synctrans/internal/relay"
	"synctrans/internal/stt"
	"synctrans/internal/translate"
)

func main() {
	addr := os.Getenv("RELAY_ADDR")
	if addr == "" {
		if p := os.Getenv("PORT"); p != "" {
			addr = ":" + p
		} else {
			addr = ":8080"
		}
	}

	transcriber, err := stt.NewOpenAIRealtime()
	if err != nil {
		log.Fatalf("stt: %v", err)
	}
	translator := translate.New() // reads ANTHROPIC_API_KEY

	srv := relay.New(transcriber, translator)

	mux := http.NewServeMux()
	mux.Handle("/ws", srv.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	httpSrv := &http.Server{Addr: addr, Handler: mux}

	// Graceful shutdown on Ctrl-C.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	go func() {
		log.Printf("relay listening on %s (ws path /ws)", addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down…")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
}
