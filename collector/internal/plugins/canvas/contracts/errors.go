package contracts

// ErrorResponse is the stable failure envelope returned by Canvas APIs.
// Error contains a safe client-facing message, never raw command output,
// private paths, stack traces, or internal error details.
type ErrorResponse struct {
	OK             bool    `json:"ok"`
	Code           string  `json:"code"`
	Error          string  `json:"error"`
	Field          string  `json:"field,omitempty"`
	ActualRevision *uint64 `json:"actualRevision,omitempty"`
}
