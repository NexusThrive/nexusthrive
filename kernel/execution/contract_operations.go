package execution

import (
	"context"
	"encoding/json"

	kernelv1 "github.com/nexusthrive/nexusthrive/kernel/gen/kernel/v1"
	"github.com/nexusthrive/nexusthrive/kernel/model"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const operationExchangeWrite = "kernel.exchange.write"
const operationEventRead = "kernel.event.read"

// contractOperations consumes descriptor policies so gateways cannot drift from
// the authority declared in the contract. NewEngine validates every declaration.
func contractOperations() []Operation {
	methods := kernelv1.File_kernel_v1_kernel_proto.Services().ByName("KernelService").Methods()
	ops := make([]Operation, 0, methods.Len())
	for i := 0; i < methods.Len(); i++ {
		policy, _ := proto.GetExtension(methods.Get(i).Options(), kernelv1.E_Operation).(*kernelv1.OperationPolicy)
		risk := map[kernelv1.Risk]model.Risk{kernelv1.Risk_RISK_READ: model.RiskRead, kernelv1.Risk_RISK_WRITE: model.RiskWrite, kernelv1.Risk_RISK_MONEY: model.RiskMoney}[policy.GetRisk()]
		ops = append(ops, Operation{Name: policy.GetName(), Permissions: policy.GetPermissions(), Capability: policy.GetCapability(), Risk: risk, Idempotent: policy.GetIdempotent()})
	}
	return ops
}

// WriteExchange interprets the original JSON body and uses a fixed handler, so
// the idempotency fingerprint cannot describe a different input from the write.
func (e *Engine) WriteExchange(ctx context.Context, call Call) (Response, error) {
	if _, err := model.CanonicalJSON(call.Request); err != nil {
		return Response{}, err
	}
	var request kernelv1.WriteExchangeRequest
	if err := protojson.Unmarshal(call.Request, &request); err != nil {
		return Response{}, model.NewError(model.CodeInvalidInput, "invalid exchange request", nil)
	}
	if key := request.GetIdempotencyKey(); key != "" {
		if call.IdempotencyKey != "" && call.IdempotencyKey != key {
			return Response{}, model.NewError(model.CodeInvalidInput, "idempotency keys must match", nil)
		}
		call.IdempotencyKey = key
	}
	return e.runIdempotent(ctx, call, operationExchangeWrite, func(ctx context.Context, tx *Tx) (json.RawMessage, error) {
		commitment := request.GetCommitment()
		if commitment == nil || commitment.GetDueAt() == nil || commitment.GetDueAt().CheckValid() != nil {
			return nil, model.NewError(model.CodeInvalidInput, "a commitment with a valid due date is required", nil)
		}
		cm := model.NewCommitment{Kind: "kernel.commitment.created", DebtorParticipantID: commitment.GetDebtorParticipantId(), CreditorParticipantID: commitment.GetCreditorParticipantId(), ResourceID: commitment.GetResourceId(), DueAt: commitment.GetDueAt().AsTime()}
		if start := commitment.GetStartsAt(); start != nil {
			if err := start.CheckValid(); err != nil {
				return nil, model.NewError(model.CodeInvalidInput, "invalid commitment start date", nil)
			}
			cm.StartsAt = start.AsTime()
		}
		var err error
		cm.Amount, err = model.ParseAmount(commitment.GetAmount())
		if err != nil {
			return nil, err
		}
		ex := model.NewExchange{Kind: "kernel.exchange.recorded", Memo: request.GetMemo()}
		for _, entry := range request.GetEntries() {
			amount, err := model.ParseAmount(entry.GetAmount())
			if err != nil {
				return nil, err
			}
			ex.Entries = append(ex.Entries, model.NewEntry{AccountID: entry.GetAccountId(), ResourceID: entry.GetResourceId(), Amount: amount})
		}
		exchange, err := tx.RecordExchange(ctx, ex)
		if err != nil {
			return nil, err
		}
		cm.OriginRef = exchange.ID
		stored, err := tx.CreateCommitment(ctx, cm)
		if err != nil {
			return nil, err
		}
		payload, err := json.Marshal(map[string]string{"exchange_id": exchange.ID, "commitment_id": stored.ID})
		if err != nil {
			return nil, err
		}
		event, err := tx.AppendEvent(ctx, EventInput{Type: "kernel.exchange.recorded", Version: 1, SubjectRef: exchange.ID, Payload: payload}, model.SubscriberInternal, model.SubscriberWebhooks)
		if err != nil {
			return nil, err
		}
		return protojson.Marshal(&kernelv1.WriteExchangeResponse{ExchangeId: exchange.ID, CommitmentId: stored.ID, EventId: event.ID})
	})
}

// ReadEvents reads an inclusive cursor page with the fixed contract policy.
func (e *Engine) ReadEvents(ctx context.Context, call Call, fromSeq int64, limit int) ([]model.Event, error) {
	var events []model.Event
	err := e.run(ctx, call, operationEventRead, func(ctx context.Context, tx *Tx) error {
		var err error
		events, err = tx.ReadEvents(ctx, fromSeq, limit)
		return err
	})
	if err != nil {
		return nil, err
	}
	return events, nil
}
