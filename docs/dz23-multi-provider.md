# Ollama DZ23 multi-provider mode

Multi-provider mode is opt-in and keeps the original Ollama local runtime unchanged. It adds remote models to the same Ollama and OpenAI-compatible listener used by local models. The DZ23 Desktop selector groups local models, automatic routes, and each configured API provider; entries without a credential remain visible but disabled.

## Desktop: configure API keys

Open **Settings → Provedores e chaves de API**, choose a provider, paste the key,
and select **Salvar chave**. Existing keys are never returned to the browser.
The provider list distinguishes missing, saved (not yet verified), disabled, and
externally managed credentials. Keys saved here take effect without restarting
inference. The Windows store uses current-user DPAPI; other platforms use
owner-only credential files. Keys from the Windows configurator can also be edited here. Other environment overrides retain precedence and cannot
be silently replaced from this screen.

The credential endpoint belongs only to the desktop server, requires the desktop
session token, rejects non-loopback and cross-origin requests, and is never
registered on the public inference listener. Saving a key does not validate an
account or make a paid API call. Verify a provider with a normal chat request.

Windows installations automatically discover the installer-owned provider config
when the launching terminal has a stale environment. An explicit
`OLLAMA_DZ23_CONFIG` still wins. The installer offers startup at Windows sign-in.

## Codex connection recovery

Adding Ollama models to the regular Codex/ChatGPT profile routes that profile
through the local Ollama service. Keep Ollama running while that integration is
active. To return to the original connection, use
`ollama launch chatgpt --restore` and restart Codex. Do not delete authentication
or conversation databases to fix a stopped local proxy. The separate Codex CLI
profile remains available for intentional Ollama sessions.

## Enable

Copy `examples/dz23-providers.json`, choose the exact model identifiers offered by your accounts, and set only the credentials you intend to use:

```sh
export OLLAMA_DZ23_CONFIG=/absolute/path/to/providers.json
export OPENAI_API_KEY=...
export ANTHROPIC_API_KEY=...
export GEMINI_API_KEY=...
export DEEPSEEK_API_KEY=...
export OPENROUTER_API_KEY=...
export OLLAMA_DZ23_LOCAL_MODEL=qwen3-coder:latest
ollama serve
```

Never commit populated environment or credential files. Configured models remain visible without credentials and use the `provider-unavailable` family marker; requests are routed only after the corresponding `api_key_env` exists.

Every credential also supports an `_FILE` companion (for example, `GROQ_API_KEY_FILE`). On Linux/macOS the file must be readable only by its owner. The Windows installer includes **Ollama DZ23 - Configure APIs**, which stores the key as a current-user DPAPI-encrypted file and sets only the file location in the user environment; it does not persist the plaintext key. Restart Ollama DZ23 after changing a credential.

The bundled Desktop configuration is kept in the current user's application-data directory and is preserved by upgrades and uninstall. It is local-only by default, so the Desktop can call remote providers without storing a second gateway token. If you bind Ollama to a non-loopback interface or put it behind a reverse proxy, add `"gateway_api_key_env": "OLLAMA_DZ23_GATEWAY_KEY"` to the configuration and set a long random value before exposing the listener.

## Use

All configured models are returned by `GET /v1/models` and `GET /api/tags`. Model names are namespaced to avoid collisions:

```sh
curl http://localhost:11434/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"deepseek/deepseek-chat","messages":[{"role":"user","content":"Hello"}]}'
```

Local model names continue to use the original Ollama handlers. Remote requests preserve streaming and safe response headers while replacing any client authorization with the provider credential.

Each provider declares its supported `paths`. OpenAI-compatible routes are passed through only when listed; native Ollama chat/generate requests are translated to chat completions and returned as Ollama JSON or NDJSON. Providers with `"type": "anthropic"` use the native Anthropic Messages protocol and translate ordinary text chat in both directions, including streaming. This prevents sending a chat-only provider an embeddings, Messages, or Responses request that it cannot implement.

Virtual aliases select the highest-priority available model supporting the requested capability:

- `auto/coding`
- `auto/reasoning`
- `auto/vision`
- `local/private` maps only to `OLLAMA_DZ23_LOCAL_MODEL` and never routes remotely

Provider and model priorities are additive. Privacy-sensitive callers should use a concrete local model or `local/private`.

## Security

- Provider endpoints require HTTPS.
- Private, loopback, unspecified, and link-local destinations are rejected after DNS resolution unless `allow_private` is explicitly enabled; approved public addresses are pinned for the request to prevent DNS rebinding.
- API keys are read from environment-variable names and are never serialized in the model catalog or safe registry snapshot.
- Client `Authorization` headers are not forwarded to providers.
- When `gateway_api_key_env` is configured, every remote-provider request must send that bearer token, including requests arriving through a loopback reverse proxy. Without it, only direct loopback clients may use paid/remote providers.
- Request and response sizes are bounded.
- Only explicitly supported inference paths are eligible for remote routing.

`allow_private` is intended for an administrator-controlled local service. It must not be enabled for user-supplied URLs.

## CLI and agent catalog

`GET /api/dz23/cli-catalog` returns the curated Code, Agent, and external-client catalog. Detection uses `PATH` lookup only: it never starts a discovered program and never returns absolute executable paths. Installation is not authorization to execute.

Catalog modes have distinct meanings:

- `executor`: may become an explicitly configured subprocess adapter;
- `client`: should consume `http://localhost:11434/v1` rather than be invoked;
- `orchestrator`: coordinates tools or agents and requires a separate permission policy;
- `custom`: administrator-defined integration.

### Explicit CLI execution

A catalog entry does not grant execution. To expose a trusted CLI wrapper as a model, configure a `cli` provider with `allow_execution: true`. The executable is started directly without a shell, receives the textual prompt on standard input, has a bounded runtime/output, and must return plain text on standard output:

```json
{
  "name": "my-cli",
  "type": "cli",
  "executable": "/absolute/path/to/trusted-wrapper",
  "args": [],
  "allow_execution": true,
  "timeout_seconds": 600,
  "paths": ["/v1/chat/completions", "/v1/responses", "/api/chat", "/api/generate"],
  "models": [
    { "id": "default", "capabilities": ["chat", "coding"] }
  ]
}
```

The wrapper owns tool-specific flags and authentication. Do not point this at a shell, a user-controlled executable, or a wrapper that interpolates prompt text into commands. Streaming is deliberately rejected for generic CLI wrappers because their output protocols are not standardized.

### Client configuration

Tools classified as `client` or `compatible` should use:

```sh
export OPENAI_BASE_URL=http://localhost:11434/v1
export OPENAI_API_KEY=ollama
```

When accessing the gateway from another machine, replace `ollama` with the value of `OLLAMA_DZ23_GATEWAY_KEY` and bind Ollama only to a trusted network interface protected by firewall/TLS.

API prefixes follow the OpenAI client convention: Gemini uses `/v1beta/openai/chat/completions`, without an extra `/v1`. The Gemini sample uses `gemini-flash-latest`; model availability remains account-dependent. Provider errors in native chat are returned as a single safe Ollama error string.

## Synchronize provider catalogs on Windows

The gateway reads `models` from the user configuration at startup. Saving a key
alone does not discover upstream models. The Windows synchronization utility reads
existing user/DPAPI credentials and queries the providers' model-list APIs:

```powershell
python -m pip install -r scripts/requirements-sync.txt
# Preview; does not modify the catalog or call inference endpoints.
powershell -File scripts/dz23-sync-models.ps1
# Apply with an automatic backup and restart the installed Ollama DZ23 app.
# Finish ongoing inference requests first: restarting interrupts them.
powershell -File scripts/dz23-sync-models.ps1 -Apply -Restart
```

A desktop shortcut can invoke the latter command. Synchronization is on demand;
there is no background task and no polling of provider APIs. Run it again after
adding credentials or when providers publish new models. It requires Python 3.10+
and `httpx`; `-Python` selects a specific Python executable.

Supported catalog adapters cover the 19 bundled providers. Ollama Cloud uses
`https://ollama.com/api/tags`, Gemini uses its native paginated model endpoint,
Anthropic uses its native paginated model endpoint, and the other providers use
OpenAI-compatible model lists. Only allowlisted HTTPS hosts receive credentials;
redirects are rejected. Existing credential names and inference endpoints remain
unchanged. No credentials are written to reports or repository files.

Each successful catalog is deduplicated and obvious non-chat/inactive models are
excluded using provider metadata and identifier filters. Unknown capability metadata
is represented conservatively as chat only. Existing models retain their configured
capabilities and routing priorities; new models receive low priority so they do not
replace preferred automatic routes. Listing is **not** a guarantee of inference
access, tool support, free usage, or account balance. Some providers list public
models even with an invalid token; the report deliberately sets
`inference_tested: false` for catalog synchronization.

Failed providers retain their previous catalog and get an explicit status in
`sync-status.json`. HTTP 401/403 requires checking credentials or provider access;
the utility does not replace keys or silently switch endpoints. Configuration edits
made during the fetch cause an abort; updates use a same-directory atomic replace
and a timestamped backup. Restore that backup and restart Ollama to undo a sync.

Run the focused tests with:

```sh
python scripts/test_dz23_sync_models.py
```
