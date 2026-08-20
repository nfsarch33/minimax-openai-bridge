// runx-public-repo-gate: allow-file secret_cred_ref — minimax-api-N is the runtime alias FORMAT, not a 1Password item reference (same convention as internal/bridge/config.go)
// Package minimaxauth is a state-only key selector for MiniMax API key
// aliases: it enumerates configured aliases, records success/quota/error
// observations, writes NDJSON audit events, and picks the next alias to try
// (sticky-with-failover). It never performs network calls itself — callers
// pair the SecretLoader interface with their own secret source.
//
// Vendored from a private tooling repo so this public project builds
// standalone (the previous go.mod dependency was unresolvable for public
// users). Sync deliberately with upstream; do not hand-edit divergently.
// A shared public module is the tracked consolidation path.
package minimaxauth
