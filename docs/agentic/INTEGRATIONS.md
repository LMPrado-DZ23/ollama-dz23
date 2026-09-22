# Integrações externas e MCP

## Conectores HTTP

O runtime aceita um arquivo JSON apontado por `OLLAMA_AGENT_CONNECTORS`. O arquivo contém apenas metadados, URLs HTTPS, operações permitidas e o nome da variável de ambiente que contém o token. Para tenants autenticados, uma operação pode declarar `oauth_provider` e o runtime resolve o access token cifrado da organização; nesse modo o token não é aceito pelo input da tool. O valor do token nunca entra no repositório, missão, memória, evento ou resposta de capabilities.

```bash
export OLLAMA_AGENT_CONNECTORS=/etc/ollama-dz23/agent-connectors.json
export DZ23_GITHUB_TOKEN='...'
export DZ23_GOOGLE_TOKEN='...'
export DZ23_SLACK_TOKEN='...'
export DZ23_DISCORD_TOKEN='...'
export DZ23_WHATSAPP_TOKEN='...'
```

O exemplo [agent-connectors.json](../../examples/agent-connectors.json) cobre GitHub, Google Workspace, Slack, Discord e WhatsApp Cloud. Cada chamada exige uma operação declarada, método permitido e prefixo de caminho permitido. Redirects são desativados, o timeout é limitado e a resposta é limitada a 2 MiB. Operações de escrita são tools de efeito externo e exigem approval do runtime.

O fluxo de autenticação OAuth usa PKCE, state one-time, armazenamento AES-GCM e refresh server-side. Configure os endpoints do provider e `OLLAMA_AGENT_CREDENTIAL_KEY` fora do repositório. Tokens pessoais por `token_env` continuam disponíveis apenas para desenvolvimento ou conectores explicitamente não multiusuário.

## SAML enterprise

O SP SAML é configurado por `OLLAMA_AGENT_SAML_<PROVIDER>_IDP_METADATA_URL`, `_METADATA_URL`, `_ACS_URL`, `_ENTITY_ID`, `_SP_PRIVATE_KEY_FILE`, `_SP_CERTIFICATE_FILE` e `_DEFAULT_REDIRECT_URI`. O metadata do IdP é validado pelo pacote `crewjam/saml`; o runtime assina AuthnRequests, usa RelayState one-time e valida o ACS antes de provisionar usuário e organização. O fluxo de primeiro login só fica público com `OLLAMA_AGENT_AUTH_SSO_PUBLIC=true`. Certificados, chaves e URLs devem ser provisionados pelo operador, e o teste ponta a ponta depende de um IdP real.

## MCP stdio

O runtime aceita um arquivo JSON apontado por `OLLAMA_AGENT_MCP`:

```json
[
  {
    "id": "filesystem-readonly",
    "command": "/usr/local/bin/my-mcp-server",
    "args": ["--readonly"],
    "allowed_methods": ["initialize", "tools/list", "tools/call"],
    "environment_vars": ["MCP_HOME"],
    "timeout_seconds": 30
  }
]
```

O processo é iniciado apenas quando uma chamada é feita. O manager usa JSON-RPC por stdin/stdout, limita methods, repassa somente variáveis explicitamente declaradas, desabilita shell e encerra o processo em timeout/cancelamento. O `mcp.call` é uma tool de efeito externo e exige approval.

O isolamento de filesystem e rede do servidor MCP ainda deve ser reforçado com um executor sandbox dedicado em instalações multiusuário. Nunca registre um MCP com `environment_vars` que incluam credenciais sem uma policy de tenant e auditoria equivalente.


## Infraestrutura distribuída

O runtime permanece local-first por padrão. Para persistência compartilhada, defina `OLLAMA_AGENT_DATABASE_URL` com uma URL PostgreSQL; o servidor executa migrações idempotentes para `agent_missions` e `agent_events`, preservando o JSON store como fallback quando a variável não existe. Para workers compartilhados, defina `OLLAMA_AGENT_REDIS_URL` e opcionalmente `OLLAMA_AGENT_REDIS_PREFIX`; a fila Redis implementa enqueue, claim, retry com backoff, dead-letter e replay. Não configure uma URL de produção com credenciais embutidas em arquivos versionados.

Para traces distribuídos, defina `OLLAMA_AGENT_OTLP_ENDPOINT` com uma URL HTTPS de OTLP HTTP. O provider exige HTTPS por padrão; `OLLAMA_AGENT_OTLP_ALLOW_INSECURE=1` é reservado para desenvolvimento local. A stack de desenvolvimento em `deploy/docker-compose.agentic.yml` fornece PostgreSQL, Redis e OpenTelemetry Collector.

## Companion WebSocket e mTLS

O endpoint `GET /api/agent/v1/devices/:id/connect` aceita WebSocket somente sobre TLS, salvo `OLLAMA_AGENT_ALLOW_INSECURE_COMPANION=1` para loopback local. `OLLAMA_AGENT_TLS_CERT_FILE` e `OLLAMA_AGENT_TLS_KEY_FILE` ativam TLS 1.3 no listener e recarregam o certificado por handshake; `OLLAMA_AGENT_REQUIRE_MTLS=1` exige também `OLLAMA_AGENT_TLS_CLIENT_CA_FILE`. O primeiro frame precisa ser `hello` com `device_id`, `token` e capabilities. `OLLAMA_AGENT_COMPANION_ORIGINS` limita Origins explícitas; a lista não deve ser `*` em produção.

O protocolo inicial é `dz23-companion.v1` e suporta `hello`, `heartbeat`, `ping` e respostas de rejeição para tipos ainda não habilitados. A camada de transporte não executa comandos arbitrários: ações de tela, teclado, processos e arquivos continuam sujeitas ao registry de tools, scopes e approvals do runtime.

## Publicação de builders

Configure `OLLAMA_AGENT_DEPLOYMENTS` apontando para um JSON como [`examples/agent-deployments.json`](../../examples/agent-deployments.json). Os adapters `vercel` e `netlify` usam as APIs oficiais; `generic` envia um payload de arquivos base64 para `/deploy`, permitindo integrar AWS, Cloudflare, um pipeline interno ou outro hosting sem colocar SDKs e credenciais no binário. Os tokens são lidos de `token_env`, e uma publicação exige approval no endpoint. O código cria a requisição de publicação e valida o workspace, mas credenciais de conta, domínio, projeto, DNS, billing e permissões de hosting continuam responsabilidade do operador.
