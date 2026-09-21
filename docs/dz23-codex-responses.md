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

## Tool continuation metadata

Some providers, including current Gemini thinking models, return opaque tool
context in `tool_calls[].extra_content`. Dropping that context can make the
second request fail even when the first generation and tool call succeeded.
DZ23 captures this context before native/Responses conversion and restores it
on the matching assistant tool call in subsequent requests. The cache survives
server restarts and is bound to provider destination, credential, model, call
ID, function name and canonical arguments; it never borrows metadata from a
different provider, model, credential or changed tool call.

The local user cache stores only provider-supplied opaque metadata, not prompts,
API keys or tool results. On Windows it uses user-bound DPAPI. Cache retention
is bounded to 30 days, 2,048 records and 32 MiB. A client replay older than the
retention window may require a new conversation. No dummy signatures or
signature-validation bypasses are inserted. Codex command approvals and sandbox
policies remain in force; completing model inference is not proof that a
particular host command was permitted or executed.
