package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/execution"
	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// Operation names the gateway registers. They live in the ledger namespace, so
// the exchanges and events they write carry the same prefix (the engine
// requires it).
const (
	opTransfer = "ledger.transfer"
	opBalances = "ledger.balances"
	opEvents   = "ledger.events"
)

// Operations are the declarations the gateway needs the engine to know. The API
// binary passes them to execution.NewEngine.
func Operations() []execution.Operation {
	return []execution.Operation{
		{Name: opTransfer, Permissions: []string{model.PermissionExchangeWrite, model.PermissionLedgerRead}, Risk: model.RiskMoney, Idempotent: true},
		{Name: opBalances, Permissions: []string{model.PermissionLedgerRead}, Risk: model.RiskRead},
		{Name: opEvents, Permissions: []string{model.PermissionEventRead}, Risk: model.RiskRead},
	}
}

// transferRequest is the body of a transfer.
type transferRequest struct {
	FromAccountID string `json:"from_account_id"`
	ToAccountID   string `json:"to_account_id"`
	Amount        string `json:"amount"`
	Memo          string `json:"memo"`
}

// transferHandler moves amount between two accounts of one resource: one
// exchange that sums to zero, one event and its outbox row, all in the
// transaction of the call.
func transferHandler(req transferRequest, amount model.Amount) execution.ResultHandler {
	return func(ctx context.Context, tx *execution.Tx) (json.RawMessage, error) {
		if req.FromAccountID == req.ToAccountID {
			return nil, model.NewError(model.CodeRuleViolation, "a transfer needs two different accounts", nil)
		}
		accounts, err := tx.ListBalances(ctx)
		if err != nil {
			return nil, err
		}
		var from, to *model.AccountBalance
		for i := range accounts {
			switch accounts[i].AccountID {
			case req.FromAccountID:
				from = &accounts[i]
			case req.ToAccountID:
				to = &accounts[i]
			}
		}
		if from == nil || to == nil {
			return nil, model.NewError(model.CodeNotFound, "an account of the transfer does not exist in this space", nil)
		}
		if from.ResourceID != to.ResourceID {
			return nil, model.NewError(model.CodeRuleViolation, "a transfer moves one resource: both accounts must hold the same", nil)
		}
		if !amount.FitsScale(from.ResourceScale) {
			return nil, model.NewError(model.CodeInvalidInput, fmt.Sprintf("the amount has more decimals than the resource allows (%d)", from.ResourceScale), nil)
		}
		exchange, err := tx.RecordExchange(ctx, model.NewExchange{
			Kind: "ledger.transfer", Memo: req.Memo,
			Entries: []model.NewEntry{
				{AccountID: to.AccountID, ResourceID: to.ResourceID, Amount: amount},
				{AccountID: from.AccountID, ResourceID: from.ResourceID, Amount: amount.Neg()},
			},
		})
		if err != nil {
			return nil, err
		}
		payload, err := json.Marshal(map[string]string{
			"exchange_id": exchange.ID, "from_account_id": from.AccountID, "to_account_id": to.AccountID,
			"amount": amount.String(), "resource": from.ResourceCode,
		})
		if err != nil {
			return nil, fmt.Errorf("marshal event payload: %w", err)
		}
		if _, err := tx.AppendEvent(ctx, execution.EventInput{Type: "ledger.transfer_recorded", Version: 1, Payload: payload}, model.SubscriberInternal); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"exchange_id": exchange.ID, "amount": amount.String(), "resource": from.ResourceCode})
	}
}

// eventView is how an event leaves the gateway.
type eventView struct {
	Seq        int64           `json:"seq"`
	Type       string          `json:"type"`
	OccurredAt time.Time       `json:"occurred_at"`
	ActorKind  string          `json:"actor_kind"`
	ActorID    string          `json:"actor_id,omitempty"`
	RequestID  string          `json:"request_id"`
	Payload    json.RawMessage `json:"payload"`
}
