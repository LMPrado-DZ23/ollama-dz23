# Entrega da fase agentic — Ollama DZ23

## Escopo implementado

Esta fase estende o runtime local-first com autenticação por organização, memberships e RBAC, state OAuth one-time com PKCE e armazenamento AES-GCM de access/refresh tokens usando chave externa. Também adiciona fila persistente com retry backoff, dead-letter queue, replay manual, tracing de missão/tool e stream SSE para eventos.

A camada de execução agora possui adapters multimídia HTTPS para imagem, vídeo, speech e transcrição, além de um gerador WAV determinístico para smoke tests. O BuilderService cria projetos de website, app, game, slides e dashboard a partir de templates ou arquivos declarados, valida path traversal, cria preview, exporta ZIP e publica uma cópia versionada localmente. O preview é servido somente dentro do root do projeto.

O Desktop companion foi separado por plataforma: Linux preserva os executáveis existentes; macOS usa `screencapture`, `cliclick`, `pbcopy`, `pbpaste` e `ps`; Windows usa PowerShell/.NET e APIs `user32` para screenshot, mouse, teclado, clipboard e processos. A colaboração local persiste comentários e presença e oferece snapshot/stream SSE por projeto. O cliente Expo recebeu SecureStore para token, configuração de build EAS e perfis Android/iOS.

## Provas executadas

A suíte completa `CGO_ENABLED=0 go test ./internal/agent -count=1` passou. Ela cobre autenticação, OAuth state e credencial cifrada, builders, colaboração, memória semântica, MCP, Browser Operator real com Playwright, sandbox isolada, fila, runtime, artifacts, scheduler e mídia. A compilação cruzada `go build ./internal/agent` passou para `GOOS=darwin GOARCH=amd64` e `GOOS=windows GOARCH=amd64`. O `npm ci` seguido de `npm run typecheck` passou em `apps/mobile-agentic`.

O gate do pacote `server` ainda é bloqueado por uma falha preexistente/ambiental no upstream MLX (`mlx/nn.go` sem símbolos `Array`, `Compile1`, `Shapeless`, e `mlxrunner/xgrammar` sem arquivos aplicáveis). Esse bloqueio não ocorreu nos pacotes `internal/agent` nem nos adapters cross-platform e precisa ser resolvido com a toolchain/branch MLX correta antes de declarar o binário completo compilável nesta máquina.

## Limites deliberados

Os adapters de mídia exigem um provider HTTPS real e credenciais configuradas; o WAV local não é geração neural. A publicação de builder é local e versionada, não deploy público. EAS exige credenciais de assinatura e configuração de loja. OAuth exige URLs de provider, nomes de variáveis de segredo e `OLLAMA_AGENT_CREDENTIAL_KEY`. O store local deve ser trocado por SQLite/PostgreSQL, fila distribuída e secrets manager antes de uso multi-tenant em produção. O smoke cross-platform comprova compilação, não interação física em um Mac ou Windows real.

## Retomada

1. Resolver o bloqueio MLX e executar o build/teste do pacote `server` e do binário Ollama completo.
2. Configurar um provider multimídia, um OAuth provider e um adapter de hosting em ambiente de staging, sempre com secrets fora do repositório.
3. Executar testes reais de browser/desktop em cada sistema e build Expo em dispositivos Android/iOS.
4. Criar commit/PR da fase, revisar o diff, publicar a branch autorizada e só então promover para produção.
