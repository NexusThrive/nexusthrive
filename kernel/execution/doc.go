// Package execution is the engine that runs every operation of the kernel: one
// operation, one transaction (ADR-0035). An operation declares its permissions,
// capability, risk and idempotency once, when the engine starts. A call opens
// the transaction with the space fixed, verifies every permission with
// capability.Authorizer inside that same transaction and before any write, and
// hands the handler a Tx that exposes only state primitives, never SQL. The
// ledger entries, commitments, events and outbox rows of the operation are
// committed together or not at all, and a panic rolls everything back.
//
// This package is the only importer of kernel/state besides state itself: the
// architecture test keeps every other package away from it, so that no road
// around the permission check exists in code that can see the pool.
package execution
