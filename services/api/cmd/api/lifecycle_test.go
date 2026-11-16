package main

import (
	"context"
	"net/http"
	"testing"
	"time"
)

type lifecycleWorker struct{ started, stopped chan struct{} }

func (w *lifecycleWorker) Run(ctx context.Context) { close(w.started); <-ctx.Done(); close(w.stopped) }

func TestAPICancellationWaitsForWorker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := &lifecycleWorker{started: make(chan struct{}), stopped: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		done <- runHTTP(ctx, &http.Server{Addr: "127.0.0.1:0", Handler: http.NotFoundHandler()}, worker)
	}()
	select {
	case <-worker.started:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("API shutdown did not finish")
	}
	select {
	case <-worker.stopped:
	default:
		t.Fatal("API returned before worker stopped")
	}
}
