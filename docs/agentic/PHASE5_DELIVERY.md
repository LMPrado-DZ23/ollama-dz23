# Entrega da fase 5 — Infraestrutura distribuída e operação segura

## Escopo implementado

O Agentic Runtime agora pode usar PostgreSQL como store compartilhado opcional por meio de `OLLAMA_AGENT_DATABASE_URL`. O adapter cria tabelas idempotentes de missões e eventos, usa upsert para missões, append-only com idempotência para eventos e mantém o JSON store local quando PostgreSQL não está configurado.

A fila Redis opcional é ativada por `OLLAMA_AGENT_REDIS_URL` e suporta enqueue, claim, backoff, retries, dead-letter, replay e worker persistente. O caminho local continua sendo o default. O protocolo Redis foi mantido em uma camada pequena e testável, sem credenciais em arquivos de configuração.

O runtime possui provider OpenTelemetry opcional por `OLLAMA_AGENT_OTLP_ENDPOINT`. O endpoint deve ser HTTPS, salvo override explícito para desenvolvimento. O TraceStore local continua preservando a auditoria de missão mesmo quando o collector está indisponível.

Companions podem abrir `GET /api/agent/v1/devices/:id/connect` por WebSocket. O handshake requer frame `hello` com token do device e capabilities; o transporte exige TLS por padrão, oferece verificação mTLS opcional, limita Origins e aceita heartbeat/ping. O transporte não concede execução arbitrária: tools e approvals continuam sendo a fronteira de autorização.

O mobile Expo ganhou cache de missão, fila de ações offline e sincronização automática após retorno de conectividade. A sessão continua no SecureStore e a URL do servidor no AsyncStorage.

A pipeline `.github/workflows/dz23-agentic-quality.yaml` executa gates Go, typechecks web/mobile e gera SBOM CycloneDX. `deploy/docker-compose.agentic.yml` fornece PostgreSQL, Redis e OpenTelemetry Collector para desenvolvimento local.

## Gates

Os testes `CGO_ENABLED=0 go test ./internal/agent -count=1` e `CGO_ENABLED=1 go test ./server -count=1` passaram após as alterações. O `npm ci` e `npm run typecheck` do cliente mobile também passaram. O teste de parser RESP2 e o teste do provider OpenTelemetry noop passam junto da suíte agentic.

## Limites honestos

O adapter Redis usa protocolo RESP2 e precisa de teste de integração contra Redis real, TLS Redis e política de cluster antes de produção. O PostgreSQL precisa de migração em ambiente real, pool tuning, backup, RLS por organização e testes de failover. O WebSocket exige terminação TLS real, rotação de certificados, mTLS configurado, replay protection e testes em companions físicos. O mobile offline ainda precisa de resolução de conflitos, push notification remoto e testes Android/iOS assinados. A pipeline SBOM não publica nem assina release por conta própria; os workflows de release existentes continuam sendo a autoridade de distribuição.
