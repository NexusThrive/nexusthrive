// Package scheduler runs the bounded, single-instance M2 worker inside the API.
package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/execution"
	"github.com/nexusthrive/nexusthrive/kernel/model"
)

const (
	interval         = time.Second
	passTimeout      = 30 * time.Second
	spacesPerPass    = 10
	concurrentSpaces = 4
	workPerSpace     = 100
)

// Delivery is the HTTP stage; it receives a verified internal space, never a pool.
type Delivery interface {
	DeliverSpace(context.Context, string) error
}

type workerKernel interface {
	ListWebhookSpaces(context.Context, model.Actor, string, int) ([]string, error)
	FireDueTimers(context.Context, execution.Call, int) (model.TimerRun, error)
	FanoutWebhooks(context.Context, execution.Call, int) error
}

// Runner serializes passes and advances a circular cursor across bounded pages.
type Runner struct {
	engine   workerKernel
	delivery Delivery
	log      *slog.Logger
	passMu   sync.Mutex
	cursor   string
}

// New requires the execution engine; a nil delivery keeps timers active while webhooks are disabled.
func New(engine *execution.Engine, delivery Delivery, log *slog.Logger) (*Runner, error) {
	if engine == nil {
		return nil, errors.New("scheduler needs an engine")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Runner{engine: engine, delivery: delivery, log: log}, nil
}

// Run groups ticks naturally through the ticker's bounded channel; passes never overlap.
func (r *Runner) Run(ctx context.Context) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	r.log.Info("scheduler started", "single_instance", true, "interval", interval)
	r.run(ctx, ticker.C)
}

func (r *Runner) run(ctx context.Context, ticks <-chan time.Time) {
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ticks:
			if !ok || ctx.Err() != nil {
				return
			}
			r.Pass(ctx)
		}
	}
}

// Pass attempts at most ten spaces and four concurrent workers under a shared deadline.
// A concurrent signal is coalesced instead of adding another queued pass.
func (r *Runner) Pass(parent context.Context) {
	if parent.Err() != nil || !r.passMu.TryLock() {
		return
	}
	defer r.passMu.Unlock()
	ctx, cancel := context.WithTimeout(parent, passTimeout)
	defer cancel()
	actor := model.Actor{Kind: model.ActorSystem}
	spaces, err := r.engine.ListWebhookSpaces(ctx, actor, r.cursor, spacesPerPass)
	if err != nil {
		r.report("", "directory", err)
		return
	}
	if len(spaces) == 0 && r.cursor != "" {
		spaces, err = r.engine.ListWebhookSpaces(ctx, actor, "", spacesPerPass)
		if err != nil {
			r.report("", "directory", err)
			return
		}
	}
	slots := make(chan struct{}, concurrentSpaces)
	var workers sync.WaitGroup
	for _, space := range spaces {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			workers.Wait()
			return
		}
		if ctx.Err() != nil {
			<-slots
			break
		}
		// Move only after capacity is available, so cancellation never skips unstarted work.
		r.cursor = space
		workers.Add(1)
		go func(space string) { defer workers.Done(); defer func() { <-slots }(); r.process(ctx, space) }(space)
	}
	workers.Wait()
}

func (r *Runner) process(ctx context.Context, space string) {
	call := execution.Call{Actor: model.Actor{Kind: model.ActorSystem}, SpaceID: space, RequestID: execution.NewRequestID()}
	_, err := r.engine.FireDueTimers(ctx, call, workPerSpace)
	if err != nil {
		r.report(space, "timers", err)
	}
	if r.delivery == nil || ctx.Err() != nil || suspended(err) {
		return
	}
	err = r.engine.FanoutWebhooks(ctx, call, workPerSpace)
	if err != nil {
		r.report(space, "fanout", err)
	}
	if ctx.Err() != nil || suspended(err) {
		return
	}
	if err := r.delivery.DeliverSpace(ctx, space); err != nil {
		r.report(space, "delivery", err)
	}
}

func suspended(err error) bool {
	code, typed := model.CodeOf(err)
	return typed && code == model.CodeSpaceSuspended
}

func (r *Runner) report(space, stage string, err error) {
	code := "internal"
	if typed, ok := model.CodeOf(err); ok {
		code = string(typed)
	} else if errors.Is(err, context.Canceled) {
		code = "canceled"
	} else if errors.Is(err, context.DeadlineExceeded) {
		code = "deadline_exceeded"
	}
	// Do not print raw errors: they may contain payloads or provider diagnostics.
	r.log.Warn("scheduler stage failed", "space_id", space, "stage", stage, "code", code)
}
