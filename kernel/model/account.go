package model

import "time"

// AccountClass is the accounting class of an account.
type AccountClass string

const (
	// AccountClassAsset is something the holder owns.
	AccountClassAsset AccountClass = "asset"
	// AccountClassLiability is something the holder owes.
	AccountClassLiability AccountClass = "liability"
	// AccountClassEquity is the holder's own stake.
	AccountClassEquity AccountClass = "equity"
	// AccountClassIncome is value that comes in.
	AccountClassIncome AccountClass = "income"
	// AccountClassExpense is value that goes out.
	AccountClassExpense AccountClass = "expense"
)

// Valid reports whether c is one of the known classes.
func (c AccountClass) Valid() bool {
	switch c {
	case AccountClassAsset, AccountClassLiability, AccountClassEquity, AccountClassIncome, AccountClassExpense:
		return true
	}
	return false
}

// AccountStatus is the lifecycle state of an account. An account is born open
// and closes once; it never reopens.
type AccountStatus string

const (
	// AccountStatusOpen is an account that accepts entries.
	AccountStatusOpen AccountStatus = "open"
	// AccountStatusClosed is an account kept for history that accepts no entries.
	AccountStatusClosed AccountStatus = "closed"
)

// Valid reports whether s is one of the known statuses.
func (s AccountStatus) Valid() bool {
	return s == AccountStatusOpen || s == AccountStatusClosed
}

// Account holds one resource for one participant. ParentAccountID is empty for
// a top-level account; a parent shares the resource and the space.
type Account struct {
	ID                  string
	SpaceID             string
	HolderParticipantID string
	ResourceID          string
	ParentAccountID     string
	Class               AccountClass
	Code                string
	Name                string
	Status              AccountStatus
	ClosedAt            *time.Time
}
