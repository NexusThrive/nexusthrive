package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// LookupSpaceActor calls the fixed directory without exposing a general
// unscoped query. It reads only the participant link of an already verified subject.
func LookupSpaceActor(ctx context.Context, pool *sql.DB, provider, subject, slug string) (spaceID, identityID, participantID string, found bool, err error) {
	err = inTransaction(ctx, pool, "", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "SET TRANSACTION READ ONLY"); err != nil {
			return fmt.Errorf("set directory read only: %w", err)
		}
		err := tx.QueryRowContext(ctx, `SELECT space_id,identity_id,participant_id FROM kernel.resolve_space_actor($1,$2,$3)`, provider, subject, slug).Scan(&spaceID, &identityID, &participantID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("resolve space actor: %w", err)
		}
		found = true
		return nil
	})
	if err != nil {
		return "", "", "", false, err
	}
	return
}
