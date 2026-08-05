# Collector HTTP security

All routes, including the embedded frontend, must be served through
`internal/httpsec.Guard`. New routes must not bypass the guard or emit CORS
headers. API routes require the per-run token and must use method-qualified
`http.ServeMux` patterns. Frontend requests to `/api/` must use `apiFetch` so
the per-run token is attached. Keep internal errors in server logs and return
fixed, non-sensitive messages to clients.

The token prevents requests from other browser origins; it does not protect
against local processes running as the same user, which can read the token file.
