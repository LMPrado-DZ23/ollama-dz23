# Entrega da fase 4 — Orquestração, pesquisa e contexto

## Implementações

O runtime agora possui um `AgentOrchestrator` persistente que decompõe objetivos em papéis de pesquisa, programação, testes, design, segurança, dados e revisão. Cada tarefa tem estado, tentativas, duração, output, evidências e erro próprio. O job limita número de agentes, tempo, saída e retries, aceita cancelamento de jobs planejados e produz síntese determinística com detecção de conflitos de evidência.

O `ResearchEngine` executa fontes HTTPS públicas em paralelo com limite de concorrência, cache por URL, SHA-256, extração HTML, títulos, citações, robots policy opcional e bloqueio de loopback, IP privado, link-local e hosts que resolvem para redes internas. Autenticação, CAPTCHA e takeover continuam sob responsabilidade do Browser Operator e não são contornados pelo pesquisador.

O `DeviceStore` suporta múltiplos companions, pairing code one-time com expiração, capability report, heartbeat, status online/offline, token hash-only no servidor e revogação. A API fornece endpoints para iniciar/concluir pairing, listar devices, atualizar heartbeat e revogar device.

O `DocumentIngestor` importa texto, Markdown, HTML, JSON, CSV, PDF por `pdftotext`, DOCX por XML e XLSX por worksheets, aplicando byte budget, path containment, chunking com overlap e proveniência por arquivo/URL. Cada chunk é salvo no ContextStore e pode receber embedding configurado.

A interface Web Agentic Console agora inclui criação de missão, orquestração multiagente, polling do job, exibição por papel, síntese, pesquisa profunda, citações e approvals existentes.

## Verificações

`CGO_ENABLED=0 go test ./internal/agent -count=1` passou após a fase. Os testes focados de orquestração, pesquisa, cache, SSRF, pairing, revogação, ingestão, runtime e artifacts passaram. Com GCC instalado, `CGO_ENABLED=1 go test ./server -count=1` passou. O typecheck direto do frontend (`./node_modules/.bin/tsc --noEmit`) passou; a execução direta foi usada apenas porque o pnpm bloqueia scripts de `esbuild` sem aprovação interativa, e `node_modules` foi removido depois.

## Limites restantes

O orquestrador ainda usa o runtime local como executor de subagentes e não possui workers distribuídos. O ResearchEngine não é um crawler autenticado nem substitui OCR/vision. Pairing local ainda precisa de mTLS, WebSocket, assinatura de binário e testes em computadores físicos. Ingestão de imagens exige adapter de visão. O builder continua com preview/publicação local, sem deploy público completo. A aplicação mobile ainda precisa de push, offline sync e builds assinados nas lojas.
