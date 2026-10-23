package httptransport

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestServerGracefulShutdown(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	log := zap.New(core)
	entered, release := make(chan struct{}), make(chan struct{})
	server := NewServer("127.0.0.1:0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; w.WriteHeader(204) }), log)
	shutdownStarted := make(chan struct{})
	server.RegisterOnShutdown(func() { close(shutdownStarted) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- Serve(ctx, server, log, "", "") }()
	deadline := time.After(2 * time.Second)
	for logs.FilterMessage("HTTP server starting").Len() == 0 {
		select {
		case <-deadline:
			t.Fatal("server did not start")
		case <-time.After(time.Millisecond):
		}
	}
	address := logs.FilterMessage("HTTP server starting").All()[0].ContextMap()["address"].(string)
	response := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 3 * time.Second}
		res, err := client.Get("http://" + address)
		if err == nil {
			_, err = io.Copy(io.Discard, res.Body)
			res.Body.Close()
		}
		response <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not enter handler")
	}
	cancel()
	select {
	case <-shutdownStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not start")
	}
	select {
	case err := <-result:
		t.Fatalf("server returned while handler was active: %v", err)
	default:
	}
	close(release)
	if err := <-response; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not finish")
	}
}
