# API agentic v1

A primeira API agentic roda no mesmo listener do Ollama e é local-first. Ela cria uma missão, gera um plano, aplica a policy de tools, persiste eventos e permite acompanhar o resultado.

## Configuração

Defina `OLLAMA_AGENT_ROOT` para o diretório que pode ser usado pelas missões. Por padrão, o runtime usa um diretório temporário do sistema. Defina `OLLAMA_AGENT_STORE` para o diretório persistente de missões e eventos. Se `OLLAMA_AGENT_MODEL` estiver definido, o runtime tenta usar esse modelo para gerar o plano JSON; em caso de erro, usa o planner determinístico seguro. `OLLAMA_AGENT_EMBED_MODEL` ativa memória semântica sobre a API `/api/embed`; `OLLAMA_AGENT_CONNECTORS` e `OLLAMA_AGENT_MCP` apontam para os manifestos de integração descritos em [INTEGRATIONS.md](INTEGRATIONS.md). `OLLAMA_AGENT_AUTH_STORE` habilita o store de identidade; `OLLAMA_AGENT_AUTH_REQUIRED=true` exige Bearer token; `OLLAMA_AGENT_CREDENTIAL_KEY` é obrigatório para persistir credenciais OAuth cifradas; `OLLAMA_AGENT_MEDIA_BASE_URL` e `OLLAMA_AGENT_MEDIA_API_KEY` ativam o adapter multimídia HTTPS.

```bash
export OLLAMA_AGENT_ROOT=/home/usuario/dz23-workspaces
export OLLAMA_AGENT_STORE=/home/usuario/dz23-data/agent-store
export OLLAMA_AGENT_MODEL=qwen3-coder:latest
export OLLAMA_AGENT_EMBED_MODEL=nomic-embed-text
export OLLAMA_AGENT_CONNECTORS=/etc/ollama-dz23/agent-connectors.json
export OLLAMA_AGENT_MCP=/etc/ollama-dz23/agent-mcp.json
ollama serve
```

O modelo não recebe permissão implícita. Ele somente sugere passos; o runtime valida cada passo contra o registry e a policy.

## Criar missão

```bash
curl -sS http://localhost:11434/api/agent/v1/missions \
  -H 'Content-Type: application/json' \
  -d '{"objective":"inspecionar o workspace e listar os arquivos","auto_run":true}'
```

A resposta contém `mission_id`, estado, plano, approvals e timestamps. Uma missão de leitura pode entrar em execução automaticamente quando `auto_run` é verdadeiro.

## Consultar missão e eventos

```bash
curl -sS http://localhost:11434/api/agent/v1/missions/MISSION_ID
curl -sS http://localhost:11434/api/agent/v1/missions/MISSION_ID/events
curl -N http://localhost:11434/api/agent/v1/missions/MISSION_ID/events/stream
curl -sS http://localhost:11434/api/agent/v1/missions/MISSION_ID/traces
```

Eventos são append-only no store e servem como trilha de planejamento, início, retry, sucesso, bloqueio, aprovação e conclusão.

## Artefatos

Quando uma tool produz um arquivo, a missão registra nome, caminho relativo, MIME type, tamanho e SHA-256. O download exige o identificador da missão e do artifact; o runtime revalida que o arquivo ainda está dentro do workspace.

```bash
curl -OJ http://localhost:11434/api/agent/v1/missions/MISSION_ID/artifacts/ARTIFACT_ID
```

## Aprovar ação de escrita

Passos de escrita ou terminal são marcados com `requires_approval`. A missão não executa a tool antes da aprovação.

```bash
curl -sS -X POST \
  http://localhost:11434/api/agent/v1/missions/MISSION_ID/approvals/APPROVAL_ID \
  -H 'Content-Type: application/json' \
  -d '{"approved":true,"reason":"revisado pelo operador"}'

curl -sS -X POST http://localhost:11434/api/agent/v1/missions/MISSION_ID/run
```

A rejeição torna a missão falha de forma explícita. A aprovação não é reutilizada entre missões.

## Projetos e memória

```bash
curl -sS -X POST http://localhost:11434/api/agent/v1/projects \
  -H 'Content-Type: application/json' \
  -d '{"name":"Meu projeto","root":"/workspace"}'

curl -sS -X POST http://localhost:11434/api/agent/v1/projects/PROJECT_ID/memories \
  -H 'Content-Type: application/json' \
  -d '{"kind":"decision","content":"usar testes de contrato","confidence":1,"source":"operator"}'

curl -sS 'http://localhost:11434/api/agent/v1/projects/PROJECT_ID/memories?q=contrato'
```

Memórias são persistidas por projeto, têm fonte, confiança e, quando `OLLAMA_AGENT_EMBED_MODEL` está configurado, vetor embedding. A busca usa similaridade coseno e mantém fallback lexical quando o embedder não está disponível.

## Capabilities e observabilidade

```bash
curl -sS http://localhost:11434/api/agent/v1/tools
curl -sS http://localhost:11434/api/agent/v1/connectors
curl -sS http://localhost:11434/api/agent/v1/mcp
curl -sS http://localhost:11434/api/agent/v1/metrics
curl -sS http://localhost:11434/api/agent/v1/metrics/prometheus
```

O endpoint Prometheus expõe counters de missões, passos, retries, approvals e chamadas de tool.

## Agendamentos e webhooks

Um schedule cria missões recorrentes. O intervalo é limitado a 31 dias e o claim é idempotente no store.

```bash
curl -sS -X POST http://localhost:11434/api/agent/v1/schedules \
  -H 'Content-Type: application/json' \
  -d '{"objective":"verificar o projeto","interval_seconds":3600,"webhook_secret_env":"DZ23_WEBHOOK_SECRET"}'
```

Para disparar por evento, configure a variável secreta no ambiente do processo e envie o header `X-Ollama-Agent-Secret`. O valor nunca é gravado no schedule.

```bash
export DZ23_WEBHOOK_SECRET='valor-fora-do-repositorio'
curl -sS -X POST http://localhost:11434/api/agent/v1/webhooks/SCHEDULE_ID \
  -H "X-Ollama-Agent-Secret: $DZ23_WEBHOOK_SECRET" \
  -H 'Content-Type: application/json' \
  -d '{"event":"push","ref":"main"}'
```

## Tools da primeira fatia

`workspace.list` lê entradas do workspace autorizado e limita a quantidade retornada. `workspace.read` lê no máximo 1 MiB e rejeita traversal. `workspace.write` exige approval, limita o payload, escreve com permissões restritas e cria um manifesto com tamanho e SHA-256. `terminal.exec` existe em modo conservador e só permite `pwd`, `ls` e `git status` com argumentos separados; shell interpolation e outros executáveis são bloqueados. `sandbox.exec` executa Python ou Node em user namespace, sem rede, com um bind apenas do workspace e limite de tempo/output; ainda exige approval. `browser.operator` usa Chromium/Playwright com perfil persistente por sessão, bloqueio SSRF, snapshot, click, fill, press, upload, download, screenshot e estado `approval_required` para takeover humano. `desktop.companion` possui adapters Linux, macOS e Windows para screenshot, mouse, teclado, clipboard e processos através de executáveis separados e sem shell concatenado. `mcp.call` usa JSON-RPC stdio com lifecycle, timeout, env allowlist e methods allowlisted. `connector.http` chama GitHub, Google, Slack, Discord ou WhatsApp somente por operação HTTPS declarada.

## OAuth e organizações

Com `OLLAMA_AGENT_AUTH_REQUIRED=true`, a API valida sessão, organização e RBAC antes de permitir ações. O fluxo `GET /auth/oauth/:provider/start` exige `redirect_uri` e `code_verifier` PKCE; o callback consome `state` uma única vez, troca o código no servidor e cifra access/refresh tokens com AES-GCM usando `OLLAMA_AGENT_CREDENTIAL_KEY`. Configure URLs e nomes das variáveis de segredo por provider sem colocar valores no repositório.

### SAML enterprise

`GET /auth/saml/:provider/start` cria AuthnRequest assinado e retorna `authorization_url` e RelayState one-time. `GET /auth/saml/:provider/metadata` publica o metadata do SP. `POST /auth/saml/:provider/acs` valida assinatura, audiência, destination, condições e `InResponseTo` pelo adapter `crewjam/saml`, extrai claims e cria sessão local tenant-aware. O fluxo exige `OLLAMA_AGENT_AUTH_SSO_PUBLIC=true` quando usado como primeiro login público.

As variáveis são `OLLAMA_AGENT_SAML_<PROVIDER>_IDP_METADATA_URL`, `_METADATA_URL`, `_ACS_URL`, `_SP_PRIVATE_KEY_FILE`, `_SP_CERTIFICATE_FILE`, `_ENTITY_ID` e `_DEFAULT_REDIRECT_URI`. URLs de metadata, SP e ACS devem ser HTTPS; certificados e chaves devem ser montados fora do repositório.

### TLS, mTLS e RLS

`OLLAMA_AGENT_TLS_CERT_FILE` e `OLLAMA_AGENT_TLS_KEY_FILE` ativam TLS 1.3 para o listener. `OLLAMA_AGENT_REQUIRE_MTLS=1` exige também `OLLAMA_AGENT_TLS_CLIENT_CA_FILE` e valida certificado de cliente. O certificado do servidor é recarregado a cada handshake, permitindo rotação sem reiniciar o processo. Quando o store PostgreSQL é usado, `FORCE ROW LEVEL SECURITY` e o contexto transacional `app.current_organization_id` impedem acesso entre organizações, inclusive para o dono da tabela.

## Mídia

Com um provider HTTPS compatível, use `POST /media/image`, `/media/video`, `/media/speech` e `/media/transcribe`, sempre com `mission_id`; os resultados são salvos em `.agent-media` e devolvem manifesto com hash. `POST /media/tone` gera um WAV determinístico local para smoke tests e não deve ser confundido com geração musical neural.

## Builders e preview

`POST /builders` cria `website`, `app`, `game`, `slides` ou `dashboard` a partir de arquivos controlados ou template. `POST /builders/:id/preview` valida a entrada e retorna manifesto; `GET /builders/:id/preview/*path` serve preview local com containment; `POST /builders/:id/export` cria ZIP; `POST /builders/:id/publish` publica uma cópia versionada localmente. Deploy público exige um adapter de hosting configurado e não é alegado por essa publicação local.

`POST /builders/:id/visual` atualiza componentes, bindings, estilos e eventos do canvas. `POST /builders/:id/undo` e `POST /builders/:id/redo` alteram o histórico persistido, incrementam a versão e regeneram o preview. O histórico é limitado às últimas 50 alterações para impedir crescimento sem limite.

`GET /deployments` lista somente os providers configurados sem tokens. `POST /builders/:id/deploy/:provider` empacota o workspace contido do projeto e chama Vercel, Netlify ou um deployer genérico declarado em `OLLAMA_AGENT_DEPLOYMENTS`. O corpo precisa conter `{"approved":true}`; sem essa aprovação explícita a API retorna `428 Precondition Required`. O token é lido exclusivamente de `token_env` no servidor. O adapter impõe limite de 2.000 arquivos, 50 MiB, redirects desabilitados e HTTPS fora de loopback.

## Colaboração

`GET /collab/:project_id`, `POST /collab/:project_id/comments`, `POST /collab/:project_id/presence` e `GET /collab/:project_id/stream` oferecem snapshot persistente, comentários, presença e SSE. Quando autenticação está ativa, o actor vem da sessão e o tenant é validado pelo middleware.

## Mobile

O cliente Expo em `apps/mobile-agentic` usa SecureStore para o Bearer token, polling compatível com Android/iOS e EAS profiles para development/preview/production. Assinatura, credenciais de loja, `ascAppId` e hosting da API continuam fora do código.


## Orquestração multiagente

`POST /api/agent/v1/orchestration/jobs` recebe `objective`, `roles` opcionais (`research`, `programming`, `testing`, `design`, `security`, `data`, `review`), `workspace`, `project_id` e `budget`. Se `auto_run` for verdadeiro, o job começa em background; caso contrário, a API devolve um job `PLANNED`.

```bash
curl -sS -X POST http://localhost:11434/api/agent/v1/orchestration/jobs \
  -H 'Content-Type: application/json' \
  -d '{"objective":"pesquisar e revisar a arquitetura","roles":["research","security","review"],"budget":{"max_agents":3,"max_seconds":600,"max_retries":1},"auto_run":true}'

curl -sS http://localhost:11434/api/agent/v1/orchestration/jobs/ORCH_ID
curl -sS -X POST http://localhost:11434/api/agent/v1/orchestration/jobs/ORCH_ID/run
curl -sS -X POST http://localhost:11434/api/agent/v1/orchestration/jobs/ORCH_ID/cancel
```

O orquestrador limita concorrência, tempo, tamanho de saída e retries por job. A síntese é determinística e conserva a saída de cada papel, citações e conflitos de evidência; um modelo posterior pode substituir o reducer sem remover a validação do runtime.

## Pesquisa profunda

`POST /api/agent/v1/research` baixa URLs HTTPS públicas em paralelo, limita bytes por fonte, aplica bloqueio SSRF, respeita `robots.txt` quando solicitado, usa cache por URL e retorna fontes, SHA-256, texto extraído, citações e erros individuais.

```bash
curl -sS -X POST http://localhost:11434/api/agent/v1/research \
  -H 'Content-Type: application/json' \
  -d '{"query":"arquitetura agentic","urls":["https://example.org"],"max_sources":8,"respect_robots":true}'
```

A pesquisa não acessa hosts privados, loopback ou link-local em produção. Login, CAPTCHA e fontes autenticadas continuam exigindo o fluxo Browser Operator com takeover humano; o ResearchEngine não tenta contornar autenticação.

## Dispositivos e companions

`POST /devices/pair/start` cria um código one-time com expiração curta. O companion envia esse código em `POST /devices/pair/complete` junto com plataforma, nome e capability report; o token retornado deve ser armazenado somente no dispositivo. `POST /devices/:id/heartbeat` aceita Bearer token ou `X-Device-Token` e atualiza capabilities. `GET /devices` lista somente metadados; `POST /devices/:id/revoke` revoga o token e impede novos heartbeats.

```bash
curl -sS -X POST http://localhost:11434/api/agent/v1/devices/pair/start
curl -sS -X POST http://localhost:11434/api/agent/v1/devices/pair/complete \
  -H 'Content-Type: application/json' \
  -d '{"pairing_code":"CODE","name":"Meu Desktop","platform":"darwin","capabilities":[{"name":"screen","scopes":["desktop:screen"]}]}'
```

O pairing local não substitui mTLS, assinatura de binário ou store distribuído de produção. Esses gates permanecem obrigatórios antes de expor companions em rede pública.


## Ingestão e memória documental

`POST /projects/:id/ingest` importa arquivos do root autorizado ou URLs públicas para chunks de memória. O ingestor suporta texto, Markdown, HTML, JSON, CSV, PDF via `pdftotext`, DOCX via `document.xml` e XLSX via worksheets; cada chunk conserva o caminho/URL de origem. Embeddings são produzidos pelo embedder configurado no ContextStore. Imagens exigem um adapter de visão/OCR configurado e não são tratadas como texto automaticamente.

```bash
curl -sS -X POST http://localhost:11434/api/agent/v1/projects/PROJECT_ID/ingest \
  -H 'Content-Type: application/json' \
  -d '{"paths":["docs/spec.pdf","README.md"],"chunk_size":1800,"chunk_overlap":200}'
```
