package scheduler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/execution"
	"github.com/nexusthrive/nexusthrive/kernel/model"
)

type fakeKernel struct {
	mu                 sync.Mutex
	spaces             []string
	visited, fanout    []string
	active, max, pages int
	block              <-chan struct{}
	started            chan string
	timerError         map[string]error
	fanoutError        map[string]error
	invalid            bool
}

func (f *fakeKernel) ListWebhookSpaces(_ context.Context, actor model.Actor, after string, limit int) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pages++
	if actor.Kind != model.ActorSystem || limit != 10 {
		f.invalid = true
	}
	var out []string
	for _, space := range f.spaces {
		if space > after && len(out) < limit {
			out = append(out, space)
		}
	}
	return out, nil
}
func (f *fakeKernel) FireDueTimers(ctx context.Context, call execution.Call, limit int) (model.TimerRun, error) {
	f.mu.Lock()
	f.visited = append(f.visited, call.SpaceID)
	f.active++
	if f.active > f.max {
		f.max = f.active
	}
	if call.Actor.Kind != model.ActorSystem || call.Actor.ParticipantID != "" || limit != 100 {
		f.invalid = true
	}
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 30*time.Second {
		f.invalid = true
	}
	f.mu.Unlock()
	defer func() { f.mu.Lock(); f.active--; f.mu.Unlock() }()
	if f.started != nil {
		select {
		case f.started <- call.SpaceID:
		default:
		}
	}
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return model.TimerRun{}, ctx.Err()
		}
	}
	return model.TimerRun{}, f.timerError[call.SpaceID]
}
func (f *fakeKernel) FanoutWebhooks(_ context.Context, call execution.Call, limit int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fanout = append(f.fanout, call.SpaceID)
	if limit != 100 {
		f.invalid = true
	}
	return f.fanoutError[call.SpaceID]
}

type fakeDelivery struct {
	mu     sync.Mutex
	spaces []string
}

func (f *fakeDelivery) DeliverSpace(_ context.Context, space string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.spaces = append(f.spaces, space)
	return nil
}

func TestPassBoundsConcurrencyCoalescesAndRotates(t *testing.T) {
	block := make(chan struct{})
	kernel := &fakeKernel{block: block, started: make(chan string, 20)}
	for i := 0; i < 12; i++ {
		kernel.spaces = append(kernel.spaces, fmt.Sprintf("space-%02d", i))
	}
	deliver := &fakeDelivery{}
	r := &Runner{engine: kernel, delivery: deliver, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	done := make(chan struct{})
	go func() { r.Pass(context.Background()); close(done) }()
	for i := 0; i < 4; i++ {
		select {
		case <-kernel.started:
		case <-time.After(2 * time.Second):
			t.Fatal("workers did not start")
		}
	}
	r.Pass(context.Background())
	kernel.mu.Lock()
	pages, started, max := kernel.pages, len(kernel.visited), kernel.max
	kernel.mu.Unlock()
	if pages != 1 || started != 4 || max != 4 {
		t.Fatal("overlapping or unbounded pass", pages, started, max)
	}
	close(block)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pass did not finish")
	}
	r.Pass(context.Background())
	kernel.mu.Lock()
	visited, invalid := len(kernel.visited), kernel.invalid
	kernel.mu.Unlock()
	if visited != 12 || invalid || r.cursor != "space-11" {
		t.Fatal("unfair or invalid work", visited, invalid, r.cursor)
	}
	r.Pass(context.Background())
	if r.cursor != "space-09" {
		t.Fatal("cursor did not wrap", r.cursor)
	}
}

func TestPassContinuesAfterErrorsAndNeverLogsPayloads(t *testing.T) {
	kernel := &fakeKernel{spaces: []string{"a", "b", "c"}, timerError: map[string]error{"a": fmt.Errorf("sensitive payload"), "b": model.NewError(model.CodeSpaceSuspended, "sensitive suspension", nil)}, fanoutError: map[string]error{"a": fmt.Errorf("secret credentials")}}
	var logs bytes.Buffer
	deliver := &fakeDelivery{}
	r := &Runner{engine: kernel, delivery: deliver, log: slog.New(slog.NewTextHandler(&logs, nil))}
	r.Pass(context.Background())
	if len(deliver.spaces) != 2 || r.cursor != "c" {
		t.Fatal("one error starved later stages or spaces", deliver.spaces, r.cursor)
	}
	if strings.Contains(logs.String(), "sensitive") || strings.Contains(logs.String(), "secret credentials") {
		t.Fatal("diagnostic data leaked")
	}
}

func TestRunCancelsAndDoesNotStartQueuedPass(t *testing.T) {
	kernel := &fakeKernel{spaces: []string{"a", "b"}, block: make(chan struct{}), started: make(chan string, 4)}
	r := &Runner{engine: kernel, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time, 1)
	done := make(chan struct{})
	go func() { r.run(ctx, ticks); close(done) }()
	ticks <- time.Now()
	select {
	case <-kernel.started:
	case <-time.After(2 * time.Second):
		t.Fatal("pass did not start")
	}
	ticks <- time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not cancel work")
	}
	if kernel.pages != 1 {
		t.Fatal("queued pass started during shutdown", kernel.pages)
	}
}
