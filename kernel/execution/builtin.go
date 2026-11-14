package execution

import (
	"context"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// The names of the operations the kernel registers for itself. They are the
// only production road to firing timers and claiming the outbox.
const (
	operationTimerFire   = "kernel.timer.fire"
	operationOutboxClaim = "kernel.outbox.claim"
)

// builtinOperations declares the operations of the kernel. Both are write
// operations, so a suspended or closed space pauses them (follow-up 44).
func builtinOperations() []Operation {
	return []Operation{
		{Name: operationTimerFire, Permissions: []string{model.PermissionTimerProcess}, Risk: model.RiskWrite},
		{Name: operationOutboxClaim, Permissions: []string{model.PermissionOutboxClaim}, Risk: model.RiskWrite},
	}
}

// FireDueTimers runs one firing round in the space of the call as the
// operation kernel.timer.fire: it verifies kernel.timer.process, fires up to
// limit due timers with the clock of the authorizer and writes their events
// and outbox rows in the same transaction. A failure returns an empty result.
func (e *Engine) FireDueTimers(ctx context.Context, call Call, limit int) (model.TimerRun, error) {
	var run model.TimerRun
	err := e.run(ctx, call, operationTimerFire, func(ctx context.Context, tx *Tx) error {
		var err error
		run, err = tx.fireDueTimers(ctx, limit)
		return err
	})
	if err != nil {
		return model.TimerRun{}, err
	}
	return run, nil
}

// ClaimOutbox claims up to limit due outbox rows of one subscriber in the space
// of the call as the operation kernel.outbox.claim: it verifies
// kernel.outbox.claim and claims with the clock of the authorizer. A failure
// returns no rows.
func (e *Engine) ClaimOutbox(ctx context.Context, call Call, s model.Subscriber, limit int) ([]model.OutboxRow, error) {
	var rows []model.OutboxRow
	err := e.run(ctx, call, operationOutboxClaim, func(ctx context.Context, tx *Tx) error {
		var err error
		rows, err = tx.claimOutbox(ctx, s, limit)
		return err
	})
	if err != nil {
		return nil, err
	}
	return rows, nil
}
