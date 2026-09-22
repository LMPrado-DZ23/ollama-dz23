# Entrega da fase 7 — SSO enterprise, OAuth tenant-aware, TLS e histórico visual

## Escopo implementado

Esta fase adiciona um adapter SAML baseado na biblioteca `github.com/crewjam/saml` para metadata de IdP, AuthnRequest assinado, RelayState one-time, ACS e extração de claims. O serviço valida URLs HTTPS, carrega chave privada e certificado do SP fora do repositório e provisiona o usuário e a organização por meio do mesmo AuthStore usado pelo OAuth/OIDC.

O ConnectorManager agora pode declarar `oauth_provider` e resolver o access token cifrado da organização autenticada. O token plaintext fica restrito à chamada HTTP server-side; não é retornado por capabilities, eventos, memória ou respostas públicas. O modo anterior baseado em `token_env` continua disponível para ambientes locais ou conectores explicitamente não multiusuário.

O builder visual ganhou propriedades de estilo, bindings e eventos, validação de profundidade e quantidade, histórico persistente limitado, undo/redo e endpoints correspondentes. Cada mudança regenera o preview e incrementa a versão do projeto.

O servidor ganhou configuração TLS 1.3/mTLS opcional para o listener. O certificado do servidor é recarregado por handshake, permitindo rotação sem reinício. O RLS PostgreSQL passou a usar `FORCE ROW LEVEL SECURITY` e políticas que não liberam registros sem organização para sessões tenant-scoped.

## Provas executadas

| Gate | Resultado |
|---|---|
| `gofmt` em agent, servidor e rotas | Passou |
| `CGO_ENABLED=0 go test ./internal/agent -count=1` | Passou |
| `CGO_ENABLED=1 go test ./server ./cmd/launch ./internal/multillm -count=1` | Passou |
| `npm ci && npm run typecheck` em `apps/mobile-agentic` | Passou |
| Teste tenant-aware do connector OAuth com HTTPS de teste | Incluído em `internal/agent/connectors_test.go` |
| Integração PostgreSQL/Redis/OTLP | Mantida no CI com `-tags integration`; depende dos serviços do Compose |

## Configuração principal

SAML usa `OLLAMA_AGENT_SAML_<PROVIDER>_IDP_METADATA_URL`, `_METADATA_URL`, `_ACS_URL`, `_ENTITY_ID`, `_SP_PRIVATE_KEY_FILE`, `_SP_CERTIFICATE_FILE` e `_DEFAULT_REDIRECT_URI`. Primeiro login SSO só deve ser público com `OLLAMA_AGENT_AUTH_SSO_PUBLIC=true`.

TLS usa `OLLAMA_AGENT_TLS_CERT_FILE` e `OLLAMA_AGENT_TLS_KEY_FILE`. Para mTLS, defina `OLLAMA_AGENT_REQUIRE_MTLS=1` e `OLLAMA_AGENT_TLS_CLIENT_CA_FILE`. Nunca coloque chaves, certificados privados, tokens ou DSNs com senha em commits.

## Limites

O adapter SAML está pronto para integração, mas um teste ponta a ponta ainda depende de um IdP real e de certificados provisionados pelo proprietário. O ConnectorManager resolve credenciais OAuth existentes, porém o catálogo de cada API ainda precisa declarar operações específicas e renovação pode exigir o provider OAuth correspondente. Undo/redo é persistente por projeto, mas não é colaboração CRDT. TLS/mTLS opera no listener quando configurado; não substitui instaladores assinados, pinning ou rotação de CA em produção. Exportadores PDF/DOCX/PPTX continuam sendo contratos base, não editores profissionais completos.
