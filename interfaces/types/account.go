package types

// AccountID scopes contracts from their first version. Only one account is
// currently run by the legacy host; concurrent accounts remain future work.
type AccountID string

const DefaultAccount AccountID = "default"
