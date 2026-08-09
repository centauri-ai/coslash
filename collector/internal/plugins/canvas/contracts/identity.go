package contracts

// SessionIdentity is the only supported identity for a coSlash session.
// Agent and ID together form the key; a bare ID is never globally unique.
type SessionIdentity struct {
	Agent string `json:"agent"`
	ID    string `json:"id"`
}
