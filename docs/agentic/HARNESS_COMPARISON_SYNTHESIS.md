# Síntese comparativa dos harnesses — Ollama DZ23

## Escopo e regra de proveniência

Esta síntese transforma capacidades observáveis em documentação pública de Builder.io, FlutterFlow, GitHub Copilot Workspace, OpenHands, Devin, Cline, Claude Code, Cursor, Windsurf, Replit Agent, v0, Lovable, Manus, Codex, Dify, LiteLLM, SWE-agent, AutoGen e CrewAI em requisitos independentes para o Ollama DZ23. Ela não copia código, prompts proprietários, identidade visual, pesos, índices, protocolos internos ou mecanismos não publicados. Produtos SaaS proprietários continuam sujeitos aos seus termos; licenças MIT, Apache-2.0, BSD e outras de repositórios auxiliares não licenciam o serviço inteiro, os modelos ou as marcas.

O nome **DZ23** não foi confirmado nas fontes como um modelo, hardware, alias ou endpoint público. O software deve descobrir o identificador real em `/api/tags` ou `/v1/models`, negociar capacidades e registrar o resultado antes de habilitar ferramentas, visão, reasoning ou multimodalidade.

## Padrões incorporados ao núcleo

| Família de referência | Padrão reimplementável | Destino no Ollama DZ23 | Aceitação mínima |
|---|---|---|---|
| Manus, Devin, OpenHands, Replit | Máquina de estados assíncrona: objetivo, contexto, plano, execução, validação, entrega, espera, erro e retomada | `internal/agent/runtime.go`, `queue.go`, `swarm.go` | Reiniciar o processo sem perder missão, eventos, approvals ou artefatos |
| Cline, Claude Code, Cursor, Windsurf, Codex, SWE-agent | Loop `observe → decide → validate → execute → result`, com ferramentas tipadas e contexto recuperado seletivamente | `planner.go`, `tools.go`, `context.go`, `research.go` | Tool call inválida, caminho fora do workspace e comando não allowlisted são rejeitados antes da execução |
| Cline, Claude Code, Cursor, Windsurf | Política em camadas `allow / ask / deny`, checkpoints, diff e rollback independentes do modelo | `runtime.go`, snapshots, approvals | Escrita, shell, MCP, rede, secrets, deploy, push e publicação exigem política própria |
| OpenHands, SWE-agent, E2B | Runtime intercambiável local/Docker/remoto com volumes e rede explicitamente limitados | `sandbox.exec`, `desktop.go`, `queue.go` | Processo infinito, saída excessiva, path traversal e tentativa de egress são interrompidos |
| LiteLLM, Dify, AutoGen, CrewAI | Registro de providers, aliases, capabilities, retries, cooldowns, fallback e roteamento | `multillm`, `MediaManager`, `OllamaEmbedder` | Modelo inexistente e capacidade ausente falham de forma explícita |
| OpenHands, Manus, Codex, v0 | Event stream tipado e reconectável para texto, reasoning opcional, tool calls, arquivos, terminal, erro e conclusão | SSE, JSONL, `events.go`, traces | Cliente reconecta por ID sem duplicar efeitos |
| Builder.io, FlutterFlow, v0, Lovable | Schema declarativo de conteúdo, registry de componentes, preview isolado, proposta JSON e aprovação antes da aplicação | `BuilderSpec`, `VisualComponent`, preview visual | Componente/input fora da allowlist é rejeitado; preview não recebe segredos |
| Builder.io, FlutterFlow, Lovable, Dify | Separação entre leitura pública, escrita autenticada, ambientes, secrets e publicação | builders, connectors, secrets, deploy | Chaves privadas permanecem server-side e cada ambiente tem configuração explícita |
| Manus Wide Research, AutoGen, CrewAI, SWE-agent | Fan-out/fan-in com papéis, orçamento, paralelismo limitado, síntese e conflitos explícitos | `swarm.go`, `research.go` | Falha parcial e cancelamento não corrompem a síntese nem excedem orçamento |
| Manus, Devin, Lovable, FlutterFlow | Conectores OAuth/HTTP/MCP por escopo, allowlist, revogação e auditoria | `connectors.go`, `mcp.go`, OAuth/OIDC | Token nunca aparece em prompt/log; operação não registrada é bloqueada |
| Codex, Cline, Copilot Workspace, Cursor | Git por branch/worktree, patch por arquivo, revisão, CI e entrega separada de push/PR/merge | `git-delivery` planejado | Nenhuma publicação ocorre sem aprovação e branch protegida |
| Dify, LiteLLM, Manus, Lovable | LLMOps: uso, custo/tempo, filas, p50/p95, traces, DLQ, replay e health | `metrics.go`, `traces.go`, `telemetry.go`, Redis/Postgres | Falha de infraestrutura fica `unknown`, não é declarada como sucesso |
| Manus, FlutterFlow, mobile clients | Cliente mobile com sessão segura, push, outbox offline, ETag/If-Match e reconciliação | `apps/mobile-agentic`, `push.go` | Offline não duplica mutações nem aceita estado obsoleto |

## Capacidades implementadas no snapshot atual

O snapshot atual já contém um núcleo agentic local com missões, planner, executor, approvals, artefatos com SHA-256, memória/projetos/skills, retomada, sandbox, Browser Operator Playwright, MCP stdio, Desktop companion, métricas, traces locais, conectores HTTP allowlisted, embeddings Ollama, UI Agentic Console, cliente Expo, autenticação local, organizações/RBAC/ABAC, tokens revogáveis, OAuth PKCE, PostgreSQL/RLS opcional, Redis/DLQ/replay opcional, WebSocket de companion, OpenTelemetry opcional, orquestração multiagente, pesquisa profunda com SSRF/robots/cache, pairing de devices, ingestão documental, mídia provider-agnostic, vision/OCR local quando Tesseract estiver instalado, builder de website/app/jogo/slides/dashboard, preview, publicação local, canvas visual e exportação ZIP/PDF/DOCX/PPTX.

O estado não deve ser descrito como paridade total ainda. **MFA TOTP, provisionamento OIDC, canvas visual e exportadores profissionais foram adicionados nesta continuação, mas precisam passar pelos gates completos.** SAML, instaladores assinados, distribuição real para as lojas, deploy efetivo em cada provedor cloud, multiplayer de produção, modelos locais específicos de imagem/vídeo/TTS/STT e testes físicos em todos os sistemas continuam itens de release.

## O que deve ser trazido como próxima camada

A primeira camada de produção é a comprovação, não mais a criação de stubs: levantar Postgres, Redis e OpenTelemetry Collector reais; executar integração com RLS em dois tenants; testar DLQ/replay com duas instâncias; testar WebSocket com TLS/mTLS e rotação de certificados; validar notificações Expo/FCM reais; executar cross-build e testes físicos Windows/macOS/Linux/Android/iOS; e publicar uma imagem/container com SBOM.

A segunda camada é a superfície de produto: editor visual com seleção, árvore, drag-and-drop, propriedades tipadas, undo/redo, colaboração e preview em iframe com `postMessage` autenticado; exportação OOXML/PDF com fixtures abertas em validadores; deployment adapters idempotentes para Vercel, Netlify, AWS e Cloudflare; e CI/CD com assinatura, auto-update e rollback.

A terceira camada é governança empresarial: SSO OIDC completo com userinfo/discovery, SAML via adapter licenciado e testado, MFA com recovery codes, ABAC por recurso, DLP em entradas/saídas, secret manager externo, retenção configurável, auditoria imutável e revisão de dependências/modelos/containers.

## Integração correta com Ollama

Ollama deve ser tratado como **backend de inferência substituível**, não como o orquestrador. O adaptador deve suportar descoberta de modelo, chat nativo e OpenAI-compatible, streaming, JSON Schema, visão base64 e tool calls quando o modelo real os suportar. `tool_choice`, logprobs, image URL, `n` e Responses stateful não devem ser presumidos. O gateway deve manter histórico, estado, compactação, filas e ferramentas fora do servidor de modelo.

Antes de habilitar autonomia, executar um conjunto fixo de contratos: `/v1/models`, chamada curta, streaming, cancelamento, JSON válido/inválido, tool call simples e paralela, erro de ferramenta, contexto longo, visão, embedding, retomada após restart e tentativa de prompt injection. Registrar modelo, versão, capabilities, hardware, contexto, latência p50/p95 e taxa de sucesso.

## Licenças e limites

OpenHands, CrewAI, SWE-agent e parte do LiteLLM declaram MIT; Cline e Codex declaram Apache-2.0; AutoGen separa código MIT e documentação CC BY 4.0; repositórios auxiliares de Builder, FlutterFlow CLI, Lovable MCP e v0 SDK têm licenças próprias. Dify possui licença Apache modificada com restrições adicionais. Claude Code, Cursor, Windsurf, Devin, Replit Agent, v0 hospedado, Lovable Cloud, Manus e Builder SaaS não devem ser tratados como open source. Cada dependência, imagem, plugin MCP, modelo, peso, dataset, marca e serviço requer auditoria própria.

## Decisão arquitetural

O melhor produto não é uma colagem de harnesses: é um **núcleo próprio com contratos compatíveis**, no qual o modelo propõe, o validador verifica, a policy autoriza, o executor isola, o observador mede e o humano aprova efeitos sensíveis. Essa separação permite incorporar boas ideias de cada referência sem copiar internals, sem acoplar o produto a um fornecedor e sem declarar capacidades que ainda não têm prova executável.

### Fontes primárias de referência

- [Ollama API e compatibilidade OpenAI](https://docs.ollama.com/api)
- [OpenHands](https://github.com/OpenHands/OpenHands)
- [Cline](https://github.com/cline/cline)
- [OpenAI Codex](https://github.com/openai/codex)
- [LiteLLM](https://github.com/BerriAI/litellm)
- [Dify](https://github.com/langgenius/dify)
- [SWE-agent](https://github.com/SWE-agent/SWE-agent)
- [AutoGen](https://github.com/microsoft/autogen) e [CrewAI](https://github.com/crewAIInc/crewAI)
- [Documentação pública do Manus API v2](https://open.manus.im/docs/v2/introduction)
