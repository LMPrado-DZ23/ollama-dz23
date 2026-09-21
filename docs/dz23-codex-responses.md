# Codex Desktop: Responses compatibility

The Desktop app and CLI can append different suffixes to `openai_base_url`:
`/responses` and `/v1/responses`. The DZ23 router now accepts both below
`/api/codex`, including `/responses/compact`. Native account requests keep their
native upstream; Ollama requests always reach the versioned Ollama API.

Providers with native Responses support keep their existing passthrough.
Chat-only providers use Ollama's existing Responses parser and output writer,
then the DZ23 native-chat adapter. Streaming, function tools and compaction
inference follow the same authenticated provider boundary. Codex account
credentials are never forwarded to the provider. Third-party credential errors
must not be rewritten as an instruction to sign in to Ollama.

A missing provider key produces a terminal `provider_not_configured` Responses
error, not a retryable server failure. Saved credentials are not proof of valid
account access, available quota, model entitlement, or model capabilities.

`GET /api/dz23/models` returns registry metadata for local clients only. Its
`available` field means enabled with a readable credential, not a successful
live generation. It never returns keys or credential file paths. Combine it
with `/api/tags` and `/api/show` for model-picker metadata; preserve native Codex
catalog entries and label missing credentials instead of inventing availability.

Regression checks: `go test -race ./internal/proxy ./internal/multillm`,
`go test ./server -run 'TestCodex|TestDZ23|TestResponses'`, and `go vet` on those
packages. Live tests must hit the exact Desktop `/api/codex/responses` URL,
not only the versioned route. Restart the Desktop app after replacing its
startup-loaded model catalog; changing the router does not require that restart.
