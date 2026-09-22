# Ollama DZ23 multi-provider mode

Multi-provider mode is opt-in and keeps the original Ollama local runtime unchanged. It adds remote models to the same Ollama and OpenAI-compatible listener used by local models. The DZ23 Desktop selector groups local models, automatic routes, and each configured API provider; entries without a credential remain visible but disabled.

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

## Claude and Codex Desktop integrations

The same listener can serve Claude Code, Claude Desktop, Codex CLI, and the
ChatGPT Desktop Codex mode. These clients must use a concrete namespaced model
when selecting a configured provider, for example `openai/gpt-4.1-mini` or
`anthropic/claude-sonnet-4-5`.

### Claude Code

Start Claude Code through the Ollama launcher so the Anthropic-compatible
environment is applied without writing a real provider key into Claude's
configuration:

```sh
ollama launch claude --model openai/gpt-4.1-mini
```

For a manual shell setup, use the local listener and the model name in the
ordinary Claude command:

```sh
ANTHROPIC_AUTH_TOKEN=ollama \
ANTHROPIC_API_KEY="" \
ANTHROPIC_BASE_URL=http://localhost:11434 \
claude --model openai/gpt-4.1-mini
```

When the selected model belongs to a provider with `type: "anthropic"`, DZ23
forwards `/v1/messages` using the native Anthropic protocol. When it belongs
to a provider with `type: "openai-compatible"`, the provider must explicitly
list `/v1/messages` in `paths`; DZ23 then translates Anthropic Messages to
`/v1/chat/completions` and translates normal and streaming responses back to
Anthropic format. This is the path used by Claude Code and Claude Desktop.

### Claude Desktop

On supported desktop platforms, enable the Claude Desktop integration with:

```sh
ollama launch claude-desktop
```

The launcher keeps the Claude gateway on loopback, assigns Claude-compatible
route IDs, and restores the original Claude Desktop configuration on
`--restore`. Use the Ollama Desktop Apps/Settings screen to select the
explicit provider model. Do not expose the Claude gateway port on a LAN.

### Codex CLI

Use the dedicated Ollama profile, which points Codex at the OpenAI-compatible
Responses API and writes a separate model catalog:

```sh
ollama launch codex --model openai/gpt-4.1-mini
```

The generated profile uses `wire_api = "responses"` and
`http://localhost:11434/v1/`. Models configured by DZ23 are advertised as
remote models in `/api/tags` and `/v1/models`, so Codex does not mistake them
for local weights. Restore only the managed Ollama profile with:

```sh
ollama launch codex --restore
```

### ChatGPT Desktop / Codex mode

On macOS/Windows installations that expose the ChatGPT Desktop Codex app,
connect the regular desktop profile with:

```sh
ollama launch chatgpt
```

The generated combined catalog keeps native ChatGPT models and adds the
selected DZ23 models. Requests are routed per model through the loopback Codex
proxy; native ChatGPT requests are not sent to a configured DZ23 provider.
Restore the native profile with:

```sh
ollama launch chatgpt --restore
```

The Codex Desktop router uses `/v1/responses`, keeps a separate Ollama-only
routing allow-list, strips the managed local credential before forwarding, and
does not persist ChatGPT session content.

### Provider checklist

Before selecting a model in either client, verify:

1. `OLLAMA_DZ23_CONFIG` points to the intended configuration;
2. the model appears in `curl http://localhost:11434/v1/models`;
3. the required credential environment variable is present;
4. the provider explicitly supports the path the client will use;
5. the model declares `tools` and `coding` when the client will edit files or
   execute agent tools;
6. the provider's context window is at least 64k for large coding sessions.

The gateway never forwards a client `Authorization` header to a provider. If
the listener is accessed off-host, configure `gateway_api_key_env`, use the
gateway bearer token, and protect the connection with TLS/firewall rules.
