# Ollama Classe A+

## Manual público do projeto

O **Ollama Classe A+** é a distribuição experimental do Ollama DZ23 que combina execução local de modelos, roteamento multi-provider e um runtime agentic com missões persistentes, ferramentas com aprovação, sandbox, memória, pesquisa, Browser Operator, companions, conectores, builders, observabilidade e publicação controlada. O projeto preserva a compatibilidade da base Ollama sempre que possível e evolui as superfícies agentic em camadas verificáveis.

> **Estado real:** o projeto possui uma base extensa implementada e testada localmente, mas ainda não deve ser descrito como paridade total com todos os produtos do mercado. Recursos dependentes de contas externas, hardware, certificados, lojas, modelos multimodais e ambientes distribuídos precisam de validação adicional.

## Visão geral

O produto é organizado em quatro superfícies. O servidor Ollama continua responsável pelo runtime de modelos, APIs compatíveis e gerenciamento local. O runtime agentic adiciona missões, planejamento, execução, approvals, artefatos, memória, filas e eventos. A interface web oferece o Agentic Console para operação. O cliente Expo e os companions representam a camada de operação remota, pareamento de dispositivos e notificações.

A arquitetura é local-first. Um operador pode começar apenas com o binário e um modelo local, adicionar um planner configurado, habilitar armazenamento PostgreSQL, workers Redis, OpenTelemetry, conectores, MCP, SAML, OAuth, mídia, publicação e companions conforme a necessidade. Cada capacidade opcional é configurada explicitamente e não deve receber credenciais dentro do repositório.

## Estado da versão pública

| Área | Estado atual | Observação |
|---|---|---|
| Chat e API Ollama | Implementado na base herdada | Preserva os comandos e contratos principais do Ollama. |
| Multi-provider DZ23 | Implementado | Provedores explicitamente configurados e endpoints compatíveis. |
| Missões agentic | Implementado | Plano validado, execução, eventos, recovery e artefatos. |
| Approvals e sandbox | Implementado | Tools classificadas e execução protegida por políticas do servidor. |
| Multiagente e pesquisa | Implementado localmente | Papéis, orçamento, síntese, citações, cache, robots e SSRF guard. |
| Browser Operator | Implementado como adapter Playwright | Requer Chromium e configuração de sessão/allowlist. |
| Companion | Implementado com pairing e WebSocket | Transporte TLS/mTLS e adapters por plataforma ainda exigem testes físicos. |
| Memória e ingestão | Implementado | Memória lexical/semântica e ingestão de formatos documentais suportados. |
| Builder | Implementado parcialmente | Canvas, bindings, undo/redo, preview, exportação e publicação local. |
| Deploy externo | Adapters implementados | Vercel, Netlify e generic; smoke real depende de credenciais. |
| SSO | Implementado em adapters | OAuth/OIDC e SAML exigem IdP, certificados e testes de produção. |
| Mobile | Base Expo implementada | Push, conflitos avançados, assinatura e lojas ainda dependem de ambiente real. |
| Modelos locais de mídia | Adapter configurável | Não confundir adapter multimodal com modelos locais completos já distribuídos. |

## Instalação rápida

### Linux

Use o método de instalação correspondente à versão do Ollama que você pretende operar ou compile a revisão do repositório para desenvolvimento. Para executar uma revisão local a partir do código-fonte:

```bash
git clone https://github.com/LMPrado-DZ23/ollama-classe-a-plus.git
cd ollama-classe-a-plus

# Conferir a revisão pública
 git log -1 --oneline

# Executar testes do runtime agentic
CGO_ENABLED=0 go test ./internal/agent -count=1

# Testar servidor e integrações principais
CGO_ENABLED=1 go test ./server ./cmd/launch ./internal/multillm -count=1

# Compilar o binário
CGO_ENABLED=1 go build -o ollama-classe-a-plus .
```

Para utilizar o binário compilado:

```bash
./ollama-classe-a-plus serve
```

Em instalações do sistema, substitua o nome do binário pelo caminho instalado. O runtime agentic usa o mesmo servidor Ollama e publica as rotas versionadas sob `/api/agent/v1`.

### Docker e infraestrutura distribuída

A composição de desenvolvimento está em `deploy/docker-compose.agentic.yml`. Ela fornece os serviços auxiliares usados para testar PostgreSQL, Redis e OpenTelemetry Collector. Não trate o compose de desenvolvimento como configuração de produção: troque senhas, restrinja rede, use TLS e faça backup antes de expor qualquer serviço.

```bash
docker compose -f deploy/docker-compose.agentic.yml up -d
export OLLAMA_AGENT_DATABASE_URL='postgres://usuario:senha@127.0.0.1:5432/ollama_agent?sslmode=disable'
export OLLAMA_AGENT_REDIS_URL='redis://127.0.0.1:6379/0'
export OLLAMA_AGENT_OTLP_ENDPOINT='http://127.0.0.1:4318'
./ollama-classe-a-plus serve
```

Em produção, use PostgreSQL gerenciado ou uma instância com backups e RLS revisado, Redis com autenticação e rede privada, e um collector OTLP com autenticação e retenção definida.

## Configuração essencial

O runtime pode funcionar sem configurações opcionais. O exemplo abaixo ativa uma configuração local mínima, sem armazenar segredo no código:

```bash
export OLLAMA_AGENT_ROOT="$HOME/.local/share/ollama-classe-a-plus/workspaces"
export OLLAMA_AGENT_STORE="$HOME/.local/share/ollama-classe-a-plus/store"
export OLLAMA_AGENT_MODEL='qwen3:32b'
export OLLAMA_AGENT_EMBED_MODEL='nomic-embed-text'
export OLLAMA_AGENT_AUTH_REQUIRED='true'
export OLLAMA_AGENT_AUTH_STORE="$HOME/.local/share/ollama-classe-a-plus/auth"
./ollama-classe-a-plus serve
```

### Variáveis do runtime

| Variável | Uso |
|---|---|
| `OLLAMA_AGENT_ROOT` | Raiz autorizada dos workspaces de missão e projetos. |
| `OLLAMA_AGENT_STORE` | Store local de missões, eventos e contexto. |
| `OLLAMA_AGENT_MODEL` | Modelo local usado pelo planner opcional. |
| `OLLAMA_AGENT_EMBED_MODEL` | Modelo de embeddings para memória semântica. |
| `OLLAMA_AGENT_AUTH_STORE` | Diretório do store de identidade e sessão. |
| `OLLAMA_AGENT_AUTH_REQUIRED` | Exige autenticação para o runtime HTTP. |
| `OLLAMA_AGENT_AUTH_DEV` | Modo de desenvolvimento; não habilitar em produção. |
| `OLLAMA_AGENT_AUTH_SSO_PUBLIC` | Permite início de SSO sem sessão prévia quando explicitamente habilitado. |
| `OLLAMA_AGENT_CREDENTIAL_KEY` | Chave externa usada para cifrar credenciais OAuth e MFA. |
| `OLLAMA_AGENT_DATABASE_URL` | Habilita store PostgreSQL com isolamento por organização. |
| `OLLAMA_AGENT_REDIS_URL` | Habilita fila Redis e workers compartilhados. |
| `OLLAMA_AGENT_REDIS_PREFIX` | Prefixo lógico das chaves Redis. |
| `OLLAMA_AGENT_OTLP_ENDPOINT` | Endpoint OTLP HTTP para traces distribuídos. |
| `OLLAMA_AGENT_OTLP_ALLOW_INSECURE` | Permite OTLP HTTP sem TLS apenas em desenvolvimento controlado. |
| `OLLAMA_AGENT_CONNECTORS` | Arquivo JSON de connectors HTTP allowlisted. |
| `OLLAMA_AGENT_MCP` | Arquivo JSON de servidores MCP declarativos. |
| `OLLAMA_AGENT_DEPLOYMENTS` | Arquivo JSON de providers Vercel, Netlify e generic. |
| `OLLAMA_AGENT_MEDIA_BASE_URL` | Endpoint compatível para operações multimodais. |
| `OLLAMA_AGENT_MEDIA_LOCAL` | Usa o endpoint local de mídia quando habilitado explicitamente. |
| `OLLAMA_AGENT_MEDIA_API_KEY` | Credencial de mídia fornecida somente pelo ambiente. |
| `OLLAMA_AGENT_PUSH_ENDPOINT` | Gateway de push para o cliente mobile. |

### Segurança de TLS e companions

Para ativar TLS e mTLS no listener agentic, configure certificados fora do repositório:

```bash
export OLLAMA_AGENT_TLS_CERT_FILE='/etc/ollama-classe-a-plus/tls/server.crt'
export OLLAMA_AGENT_TLS_KEY_FILE='/etc/ollama-classe-a-plus/tls/server.key'
export OLLAMA_AGENT_REQUIRE_MTLS='true'
export OLLAMA_AGENT_TLS_CLIENT_CA_FILE='/etc/ollama-classe-a-plus/tls/clients-ca.crt'
export OLLAMA_AGENT_COMPANION_ORIGINS='https://app.exemplo.com'
```

O modo inseguro de companion é reservado para loopback e testes. Nunca distribua chaves privadas no repositório público, em imagens de container ou no bundle mobile.

## Conectores e MCP

Os conectores HTTP são registrados por configuração declarativa com operações allowlisted, limites de payload, timeout e segredos fornecidos por ambiente. O exemplo está em `examples/agent-connectors.json`. Para cada organização, tokens OAuth podem ser cifrados no AuthStore e resolvidos durante a chamada sem serializar o valor em eventos ou respostas.

O cliente MCP usa JSON-RPC sobre stdio, controla lifecycle, aplica allowlist de métodos e restringe variáveis de ambiente. O arquivo de configuração deve ser tratado como código operacional: revisar binários, argumentos, diretórios, métodos permitidos e escopos antes de habilitar um servidor.

## API agentic

As rotas principais ficam em `/api/agent/v1`:

| Grupo | Exemplos |
|---|---|
| Missões | `POST /missions`, `GET /missions/:id`, `POST /missions/:id/run`, `GET /missions/:id/events`. |
| Approvals | `GET /missions/:id/approvals`, `POST /missions/:id/approvals/:approval_id`. |
| Artefatos | `GET /missions/:id/artifacts/:artifact_id`. |
| Multiagente | `POST /orchestration/jobs`, `GET /orchestration/jobs/:id`, cancelamento e síntese. |
| Pesquisa | `POST /research`, com fontes HTTPS públicas, citações, cache e SSRF guard. |
| Contexto | Projetos, memórias, ingestão e busca semântica. |
| Devices | Pairing one-time, capabilities, heartbeat, revogação e WebSocket companion. |
| Observabilidade | Métricas, SSE de eventos, traces locais e OTLP. |
| Builder | Criação, canvas visual, preview, undo/redo, exportação e publicação local. |
| Deploy | `GET /deployments` e `POST /builders/:id/deploy/:provider`, sempre com approval explícito. |

O guia técnico com payloads completos está em [`docs/agentic/API.md`](agentic/API.md).

## Publicação de sites e aplicações

Configure o arquivo `examples/agent-deployments.json` e aponte `OLLAMA_AGENT_DEPLOYMENTS` para uma cópia administrada pelo operador. O arquivo usa `token_env`; portanto, os tokens ficam no ambiente ou secrets manager, nunca no JSON versionado.

```bash
export OLLAMA_AGENT_DEPLOYMENTS="$PWD/examples/agent-deployments.json"
export DZ23_VERCEL_TOKEN='token-fora-do-repositorio'
```

Uma publicação externa precisa ser explícita:

```bash
curl -X POST \
  http://127.0.0.1:11434/api/agent/v1/builders/proj_123/deploy/vercel \
  -H 'Content-Type: application/json' \
  -d '{"approved":true,"target":"production"}'
```

O runtime valida o workspace, rejeita symlinks, limita tamanho e quantidade de arquivos, bloqueia redirects e exige HTTPS para endpoints externos. A implementação não concede automaticamente domínio, DNS, billing, projeto ou permissões de conta. O adapter generic permite integrar um gateway próprio; o contrato e a política de segurança desse gateway continuam responsabilidade do operador.

## Telas e estado visual

A captura abaixo é real da rota `/agentic`, renderizada com Chromium usando fixtures locais demonstrativas para exibir a tela sem executar ações externas:

![Mission Console atual](images/screens/agentic-console.png)

A tela de Settings atual ainda é mínima nesta revisão. Por isso o repositório também contém mockups conceituais, todos marcados como **CONCEITO** dentro da própria imagem:

| Tela | Imagem | Estado |
|---|---|---|
| Configurações e integrações | [configuration-mockup.png](images/mockups/configuration-mockup.png) | Direção visual planejada; configuração atual via API/env. |
| Builder visual | [builder-mockup.png](images/mockups/builder-mockup.png) | Canvas e histórico têm base; editor rico ainda evolui. |
| Companion mobile | [mobile-mockup.png](images/mockups/mobile-mockup.png) | Base Expo existe; push, conflitos avançados e lojas pendentes. |
| Agentic Console real | [agentic-console.png](images/screens/agentic-console.png) | Rota implementada, dados da captura são demonstrativos. |

As notas de proveniência estão em `docs/images/screens/SCREEN_CAPTURE_NOTES.md` e `docs/images/mockups/MOCKUP_NOTES.md`. Isso evita apresentar uma tela conceitual como funcionalidade concluída.

## Desktop, mobile e companions

O projeto contém a base do cliente Expo em `apps/mobile-agentic`, com sessão, missões, approvals, cache local e sincronização offline. Use `npm ci` e `npm run typecheck` antes de gerar um build. Os perfis EAS em `apps/mobile-agentic/eas.json` são ponto de partida; assinatura, certificados, credenciais de push, testes em dispositivos físicos e publicação em Google Play/App Store precisam ser configurados pelo mantenedor.

O companion Desktop possui contratos de pairing, capabilities, heartbeat e transporte WebSocket. Os adapters Linux, macOS e Windows usam implementações específicas por sistema operacional. Um build cruzado valida compilação, mas não substitui teste físico de tela, mouse, teclado, clipboard, permissões, instalador e atualização em cada sistema.

## Testes e qualidade

Antes de enviar uma alteração, execute os testes relevantes:

```bash
# Runtime agentic
CGO_ENABLED=0 go test ./internal/agent -count=1

# Servidor e integrações
CGO_ENABLED=1 go test ./server ./cmd/launch ./internal/multillm -count=1

# Build
CGO_ENABLED=1 go build -o /tmp/ollama-classe-a-plus .

# Mobile
cd apps/mobile-agentic
npm ci --no-audit --no-fund
npm run typecheck
rm -rf node_modules
```

Para testes distribuídos que exigem serviços reais, use a tag e variáveis documentadas no teste `internal/agent/distributed_integration_test.go`. O CI deve executar PostgreSQL, Redis e OpenTelemetry Collector em serviços efêmeros; uma suíte unitária verde não prova integração de produção.

Também revise `git diff --check`, rode scanners de segredos, verifique permissões dos arquivos, confirme que não há tokens em logs e valide autorização negativa por organização. Toda operação com efeito externo deve ter approval, timeout, limite e registro auditável.

## Como contribuir

Crie uma branch descritiva a partir de `main`. Para cada feature, documente o objetivo, o contrato de API, a matriz de autorização, os estados de UI, o risco, os testes, a observabilidade e o plano de rollback. Implemente primeiro um fluxo vertical funcional antes de adicionar telas estáticas ou abstrações genéricas.

Pull requests devem declarar o que foi realmente testado, quais dependências externas foram usadas e quais limitações permanecem. Não faça push de `.env`, tokens, certificados, bancos, dumps, `node_modules` ou credenciais de loja. Alterações que mudarem comportamento agentic devem atualizar API, arquitetura, roadmap, checkpoint e testes de regressão.

## Política de atualização contínua

A branch `main` será a linha pública estável. Cada rodada de melhoria deve seguir este fluxo:

1. Criar branch de trabalho e registrar o objetivo no checkpoint.
2. Auditar a implementação e reproduzir o problema antes de corrigir.
3. Implementar a alteração com teste unitário, contrato ou E2E adequado.
4. Atualizar README, documentação API, arquitetura e roadmap quando necessário.
5. Gerar ou atualizar screenshots e mockups somente quando a interface mudar.
6. Executar gates locais e CI, registrar resultados e limites.
7. Abrir PR draft ou PR de revisão na branch pública.
8. Fazer merge somente após revisão do mantenedor e deixar uma release ou tag quando a mudança for relevante.

O repositório não deve afirmar “paridade total” apenas porque existe um adapter ou um mockup. O status deve distinguir **implementado**, **parcial**, **bloqueado por ambiente**, **dependente de credenciais** e **conceitual**.

## Estrutura principal

```text
internal/agent/       runtime, planner, tools, memória, filas e adapters
server/               rotas HTTP, auth, SAML, TLS e WebSocket
app/ui/app/            UI web e Agentic Console
apps/mobile-agentic/  cliente Expo para operação mobile
docs/agentic/          arquitetura, API, integrações, roadmap e entregas
docs/images/           screenshots reais e mockups identificados
examples/              configurações sem segredos
schemas/               contratos versionados
scripts/               gates e verificações
```

## Licença, upstream e responsabilidade

Este repositório deriva de uma base Ollama e deve preservar os arquivos de licença, avisos e atribuições existentes. Antes de redistribuir binários, imagens, modelos ou instaladores, revise as licenças de cada componente e dependência. O mantenedor é responsável por configurar controle de acesso, proteção de credenciais, retenção de dados, consentimento para automações externas e conformidade aplicável ao ambiente de uso.

## Links públicos

- [Repositório Ollama Classe A+](https://github.com/LMPrado-DZ23/ollama-classe-a-plus)
- [Branch de evolução agentic](https://github.com/LMPrado-DZ23/ollama-classe-a-plus/tree/feat/dz23-claude-codex-desktop)
- [PR de revisão inicial](https://github.com/LMPrado-DZ23/ollama-classe-a-plus/pulls)
- [Arquitetura agentic](agentic/ARCHITECTURE.md)
- [API agentic](agentic/API.md)
- [Integrações](agentic/INTEGRATIONS.md)
- [Roadmap](agentic/ROADMAP.md)
- [Síntese dos harnesses](agentic/HARNESS_COMPARISON_SYNTHESIS.md)
