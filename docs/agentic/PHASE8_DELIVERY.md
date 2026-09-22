# Entrega da fase 8 — Publicação real de builders

Esta fase adiciona um `DeploymentManager` provider-agnostic, configurável por `OLLAMA_AGENT_DEPLOYMENTS`, com adapters para Vercel, Netlify e um endpoint genérico. O manager coleta apenas arquivos regulares dentro da raiz do builder, rejeita symlinks, limita o workspace a 2.000 arquivos e 50 MiB, ordena os arquivos para payloads reproduzíveis, bloqueia redirects e exige HTTPS fora de loopback.

A API expõe `GET /api/agent/v1/deployments` para listar providers sem tokens e `POST /api/agent/v1/builders/:id/deploy/:provider` para publicar um builder. A operação exige `approved: true` no corpo, usa o nome e a raiz do projeto já persistido e lê tokens somente de variáveis de ambiente do servidor. A publicação local anterior permanece disponível e não depende de credenciais.

O adapter Vercel envia arquivos base64 a `/v13/deployments`. O adapter Netlify cria o site, calcula digests SHA-1, cria o deploy e envia os bytes de cada arquivo. O adapter genérico envia `{name,target,files}` a `/deploy`, permitindo conectar um gateway próprio, AWS, Cloudflare ou outro serviço sem incluir SDKs específicos no núcleo.

## Provas

| Gate | Resultado |
|---|---|
| Smoke HTTPS genérico com bearer e payload base64 | Passou em `internal/agent/deploy_test.go` |
| Rejeição de HTTP externo | Passou |
| `CGO_ENABLED=0 go test ./internal/agent` | Executado na fase completa |
| `CGO_ENABLED=1 go test ./server ./cmd/launch ./internal/multillm` | Executado na fase completa |
| Build do binário principal com CGO | Executado na fase completa |

A integração ponta a ponta com Vercel, Netlify, AWS ou Cloudflare ainda depende de tokens, projeto, domínio e permissões reais do operador. Nenhum deploy externo é executado automaticamente durante os testes.
