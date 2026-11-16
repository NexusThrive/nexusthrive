package model

// AccountBalance is one account of a space with what it holds. It is a view for
// reads: the ledger entries stay the only source of truth.
type AccountBalance struct {
	AccountID     string `json:"account_id"`
	Code          string `json:"code"`
	Name          string `json:"name"`
	Class         string `json:"class"`
	HolderID      string `json:"holder_id"`
	HolderName    string `json:"holder_name"`
	ResourceID    string `json:"resource_id"`
	ResourceCode  string `json:"resource_code"`
	ResourceScale int    `json:"resource_scale"`
	// Balance is the exact decimal sum of the entries of the account.
	Balance string `json:"balance"`
}
