# Ollama DZ23 Agentic Platform — Arquitetura executável

## Objetivo

O Ollama DZ23 será o runtime local-first de uma plataforma agentic. O Ollama continua responsável por inferência, modelos locais e roteamento multi-provider. Uma nova camada agentic será responsável por transformar uma intenção do usuário em uma missão observável, autorizável, persistente e recuperável.

A plataforma não deve prometer que um modelo executou uma ação apenas porque gerou texto. Toda ação de ferramenta deve possuir uma chamada estruturada, uma decisão de política, um resultado e um evento auditável.

## Fronteiras

| Camada | Responsabilidade | Não deve fazer |
| --- | --- | --- |
| Model Gateway | Inferência local, providers, streaming e compatibilidade OpenAI/Anthropic | Executar comandos arbitrários ou conceder permissões |
| Mission Runtime | Plano, estados, fila, execução, observação, retry, pausa e recuperação | Esconder falhas ou ignorar aprovação |
| Policy Engine | Allowlist, scopes, limites, aprovação humana, isolamento e auditoria | Confiar apenas na interface do cliente |
| Tool Runtime | Filesystem, terminal, código, browser, desktop, MCP e conectores | Expor credenciais ou remover limites |
| Memory/Projects | Contexto persistente, documentos, decisões, embeddings e permissões | Misturar tenants ou armazenar secrets |
| Artifact Store | Arquivos gerados, hashes, versões, previews e downloads | Declarar arquivo pronto sem checksum e origem |
| Surfaces | CLI, API, Desktop, Web e Mobile | Bypassar autorização server-side |
| Integrations | GitHub, Google Workspace, e-mail, Slack, Discord, WhatsApp e webhooks | Enviar ou publicar sem escopo e confirmação quando necessário |

## Máquina de estados da missão

```text
CREATED
  -> PLANNING
  -> AWAITING_APPROVAL      (se o plano contiver ação protegida)
  -> READY
  -> RUNNING
  -> OBSERVING
  -> RECOVERING             (falha transitória ou efeito incerto)
  -> COMPLETED
  -> FAILED
  -> CANCELLED
```

Cada transição exige `mission_id`, `version`, `actor`, `reason`, timestamp e evento imutável. O executor usa compare-and-swap para evitar duas workers executando o mesmo passo.

## Execução

O planner recebe o objetivo, o contexto autorizado, os tools disponíveis e os limites da missão. Ele retorna um plano versionado com passos pequenos. O executor valida cada passo contra o Policy Engine antes de chamar o Tool Runtime. O observer registra stdout/stderr resumido, estado do processo, efeitos, artefatos, latência e erro classificado. O recovery decide entre retry idempotente, backoff, compensação, pausa para aprovação ou falha explícita.

A execução não deve usar `sh -c` com texto do modelo. Para comandos permitidos, o runtime deve receber um executável e argumentos separados. O filesystem deve ser limitado a workspaces autorizados. Network e recursos devem ter política independente.

## Ferramentas

Cada ferramenta implementa um contrato com nome, versão, schema de entrada, schema de saída, scopes, limites, classificação de risco e modo de aprovação. Os modos são `read`, `write`, `external_side_effect` e `destructive`. A política padrão permite leitura limitada, bloqueia efeitos externos e exige aprovação para publicação, envio, compra, exclusão, alteração de segurança e operações equivalentes.

A ponte MCP será um adapter. O processo MCP não recebe automaticamente todos os secrets ou todo o filesystem. Cada servidor possui uma declaração de capacidades, allowlist de métodos, timeout, limite de payload, política de rede e trilha de auditoria.

## Persistência

O núcleo deve funcionar sem banco externo usando um store local transacional para desenvolvimento. A implementação de produção poderá trocar o adapter por SQLite/PostgreSQL sem alterar os contratos. Missões, passos, approvals, events, memories, projects, skills e artifact manifests são entidades versionadas. Escritas usam arquivo temporário, `fsync` quando disponível e rename atômico.

## API inicial

Os endpoints agentic serão versionados sob `/api/agent/v1`:

| Método | Rota | Função |
| --- | --- | --- |
| `POST` | `/missions` | Criar missão e gerar plano inicial |
| `GET` | `/missions/:id` | Obter missão, plano e estado |
| `POST` | `/missions/:id/run` | Iniciar execução idempotente |
| `POST` | `/missions/:id/cancel` | Cancelar missão e passos pendentes |
| `GET` | `/missions/:id/events` | Ler eventos desde um cursor |
| `POST` | `/approvals/:id/decision` | Aprovar ou rejeitar ação protegida |
| `GET` | `/tools` | Listar tools filtradas por policy |
| `GET` | `/artifacts/:id` | Obter manifesto e download controlado |

A API nunca aceita um `tool_call` diretamente sem reavaliar autorização no servidor. O cliente pode pedir a ação, mas a decisão pertence ao runtime.

## Desktop, browser e mobile

O Desktop será um companion local pareado por capability report e heartbeat. A missão remota não recebe acesso genérico ao computador. Cada operação de mouse, teclado, tela, navegador, arquivo ou processo possui capability e scope separados.

O Browser Operator utilizará um adapter de browser controlado, com perfis isolados, cookies separados, logs de navegação e takeover explícito para login/CAPTCHA/ações sensíveis. A superfície Mobile será um cliente de missão e aprovação; operações locais do celular só existirão por um companion autorizado, nunca por suposição do servidor.

## Mídia e builders

O subsistema multimídia é provider-agnostic e aceita um endpoint HTTPS compatível para imagem, vídeo, speech e transcrição. Cada output entra no workspace da missão como artifact com tamanho, MIME e SHA-256. O modo `media.tone` existe somente como fixture determinística de áudio; não substitui um modelo generativo.

O BuilderService cria projetos de website, app, game, slides e dashboard com templates ou arquivos declarados, oferece preview local com containment, export ZIP e publicação local versionada. Um adapter de deploy público deverá implementar credenciais por organização, domínio, rollback, logs e health checks antes de ser habilitado.

## Identidade, OAuth e colaboração

AuthStore mantém organizações, memberships, RBAC, tokens revogáveis e OAuth state com PKCE. Access/refresh tokens de providers externos são cifrados com AES-GCM e uma chave somente de ambiente; nenhum token é enviado ao cliente ou escrito em eventos. O CollaborationStore mantém comentários, presença e stream SSE por project ID; produção deve trocar o store local por SQLite/PostgreSQL e aplicar isolamento de tenant em cada consulta.

## Métricas e auditoria

Cada missão emite eventos estruturados com correlação. Métricas mínimas incluem tempo de planejamento, duração por passo, retries, falhas, fila, aprovação, uso de tokens, custo estimado, bytes de artefatos e latência de tools. Logs não podem conter tokens, cookies, conteúdo secreto ou dados pessoais sem redaction.

## Definição de pronto da primeira fatia

A primeira fatia foi considerada pronta quando uma missão textual pôde ser criada por API, persistir plano e eventos, executar uma ferramenta de leitura limitada do workspace, produzir um artifact manifest, sobreviver a restart, bloquear um passo que exige aprovação e expor o estado por API. Browser, desktop, mídia, builders, conectores, OAuth e mobile agora possuem adapters/testes focados; a definição de produção ainda exige credenciais, deploy, assinatura nativa, testes em dispositivos reais e auditoria independente.


## Orquestração multiagente

O AgentOrchestrator decompõe um objetivo em papéis independentes, limita concorrência por job, aplica orçamento de tempo/saída/retries, cancela tarefas pelo contexto e persiste o estado de cada subagente. A síntese preserva as saídas por papel, evidencia citações e sinaliza conflitos; ela não transforma falha parcial em sucesso total.

## Pesquisa profunda

O ResearchEngine recebe URLs declaradas, limita fontes e bytes, bloqueia loopback/private/link-local, pode consultar robots.txt, mantém cache por URL e retorna texto extraído, hash, status, erros e citações. O browser continua sendo o adapter para login, CAPTCHA e fontes autenticadas; o pesquisador não contorna controle de acesso.

## Pareamento de dispositivos

DeviceStore mantém companions múltiplos por organização, pairing code one-time, capabilities, heartbeat, status online/offline e revogação. O token é entregue somente na conclusão do pairing e armazenado como hash no servidor. mTLS, assinatura de binários, auto-update e testes em hardware real continuam gates de produção.


## Infraestrutura de produção

O runtime seleciona PostgreSQL para missões/eventos e Redis para fila quando as URLs correspondentes estão configuradas; sem elas, conserva implementações locais para desenvolvimento. Essa seleção ocorre no bootstrap do servidor, não no planner, e falha explicitamente quando uma URL configurada não pode ser validada ou conectada.

O TraceStore local e o provider OpenTelemetry coexistem: o primeiro serve auditoria/replay do produto e o segundo exporta spans para observabilidade distribuída. Companions usam WebSocket versionado com handshake de device, TLS/mTLS opcional obrigatório por policy e heartbeat; o canal de transporte não é uma autorização para chamar ferramentas.
