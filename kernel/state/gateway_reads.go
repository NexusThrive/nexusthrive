package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// FindParticipantByIdentity returns the active participant of the current space
// that stands for the identity (provider, subject). found is false when the
// identity has no participant here, is disabled or the participant is archived.
// Row level security limits the search to the space fixed by InSpace, so an
// identity of another space is never seen.
func FindParticipantByIdentity(ctx context.Context, tx *sql.Tx, provider model.IdentityProvider, subject string) (participantID string, found bool, err error) {
	err = tx.QueryRowContext(ctx,
		`SELECT p.id FROM kernel.participants p
		   JOIN kernel.identities i ON i.id = p.identity_id
		  WHERE i.provider = $1 AND i.subject = $2
		    AND i.disabled_at IS NULL AND p.status = 'active'`,
		string(provider), subject).Scan(&participantID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("find participant by identity: %w", err)
	}
	return participantID, true, nil
}

// ListAccountBalances returns every account of the current space with the sum of
// its entries, ordered by code. It reads only, so it works in a read only
// transaction.
func ListAccountBalances(ctx context.Context, tx *sql.Tx) ([]model.AccountBalance, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT a.id, a.code, a.name, a.class, p.id, p.display_name,
		        r.id, r.code, r.scale, COALESCE(SUM(e.amount), 0)::text
		   FROM kernel.accounts a
		   JOIN kernel.participants p ON p.id = a.holder_participant_id
		   JOIN kernel.resources r ON r.id = a.resource_id
		   LEFT JOIN kernel.entries e ON e.account_id = a.id
		  WHERE a.status = 'open'
		  GROUP BY a.id, p.id, r.id
		  ORDER BY a.code, a.id`)
	if err != nil {
		return nil, fmt.Errorf("list account balances: %w", err)
	}
	defer rows.Close()
	var out []model.AccountBalance
	for rows.Next() {
		var b model.AccountBalance
		if err := rows.Scan(&b.AccountID, &b.Code, &b.Name, &b.Class, &b.HolderID, &b.HolderName,
			&b.ResourceID, &b.ResourceCode, &b.ResourceScale, &b.Balance); err != nil {
			return nil, fmt.Errorf("scan account balance: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
