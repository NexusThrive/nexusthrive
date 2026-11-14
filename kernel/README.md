# kernel

The NexusThrive core, written in Go. It knows nothing about any business, community or person: it structures participants, resources, exchanges and commitments, and enforces the five kernel laws (conservation, memory, commitment, permission, motion).

Planned packages:

```
kernel/
├── gateway/        Connect API, MCP and webhook endpoints
├── capability/     identity, spaces, grants and permission checks
├── execution/      operation engine, transactions and idempotency
├── extension/      registration API for the platform layer and distributions
├── model/          the seven primitives
├── state/          persistence, ledger conservation and events
├── coordination/   timers, outbox dispatch and scheduler runs
├── laws/           tests that enforce the five laws
└── db/             migrations and sqlc queries for the `kernel` schema
```

Rules:

- No business vocabulary in this module (`customer`, `invoice`, `sale`, `membership`…). An architecture test enforces it.
- This module never imports `platform/` or `distributions/`. They register themselves through `extension/`.

Current state of the engine: `execution.Engine` runs every operation in one transaction. An operation is declared in Go with its permissions, capability, risk and idempotency; `Run` fixes the space, verifies every permission with `capability.Authorizer` inside the same transaction and before any write, and gives the handler a `Tx` with no raw SQL. Only `kernel/state` and `kernel/execution` may import `kernel/state` (an architecture test enforces it). Timers and the outbox are fired and claimed through `Engine.FireDueTimers` and `Engine.ClaimOutbox` (ADR-0035). Idempotent operations (required for `money`) run only through `Engine.RunIdempotent`: it verifies permissions, takes an advisory lock on (space, key), and either replays the stored response, answers `idempotency_conflict` for another fingerprint, or runs the handler and stores an append-only record in the same transaction (ADR-0026). `Run` refuses an idempotent operation.

Current state of timers: `Engine.FireDueTimers` fires the due expiry timers of a space and expires their commitments; nothing calls it in production until the scheduler process exists (see `docs/02-arquitectura/kernel.md`, section "Temporizadores y vencimiento").

Documentation (Spanish): `docs/02-arquitectura/kernel.md`.
