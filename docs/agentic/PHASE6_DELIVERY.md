# Entrega da fase 6 — endurecimento, multimídia e builder visual

## Implementado nesta continuação

A fase adiciona MFA TOTP com segredo cifrado em repouso, verificação server-side por request e redaction do ciphertext nas respostas; provisionamento inicial OIDC por userinfo HTTPS, criação idempotente de usuário/organização e renovação de credencial já cifrada; adapter SAML baseado em `crewjam/saml` com metadata do IdP validada, AuthnRequest assinado, RelayState one-time, ACS e mapeamento de claims; OCR local opcional via Tesseract com path containment, limites e manifesto de artifact; canvas visual declarativo com componentes ricos, propriedades, filhos, bindings, eventos, preview HTML isolado, manifesto `visual.json`, versionamento e histórico persistente undo/redo; exportação profissional mínima e verificável para PDF, DOCX e PPTX, além do ZIP existente; TLS 1.3/mTLS opcional com recarga de certificado por handshake para companions; RLS PostgreSQL forçado também para o dono da tabela; e a síntese comparativa dos padrões reimplementáveis dos harnesses avaliados.

## Endpoints adicionados ou ampliados

| Método | Endpoint | Função |
|---|---|---|
| `POST` | `/api/agent/v1/auth/mfa/enable` | Habilita TOTP usando segredo Base32 fornecido pelo operador autenticado. |
| `POST` | `/api/agent/v1/auth/mfa/disable` | Remove MFA da conta autenticada. |
| `POST` | `/api/agent/v1/media/ocr` | Executa Tesseract local quando instalado e gera artifact textual. |
| `POST` | `/api/agent/v1/builders/:id/visual` | Persiste árvore visual, regenera preview e incrementa a versão. |
| `POST` | `/api/agent/v1/builders/:id/undo` | Desfaz a última alteração visual persistida. |
| `POST` | `/api/agent/v1/builders/:id/redo` | Refaz a última alteração visual desfeita. |
| `POST` | `/api/agent/v1/builders/:id/export/pdf` | Gera PDF mínimo com conteúdo do projeto. |
| `POST` | `/api/agent/v1/builders/:id/export/docx` | Gera pacote DOCX OOXML mínimo. |
| `POST` | `/api/agent/v1/builders/:id/export/pptx` | Gera pacote PPTX OOXML mínimo. |

O fluxo OAuth existente agora aceita `OLLAMA_AGENT_OAUTH_<PROVIDER>_USERINFO_URL`. O provisionamento público de primeiro login só é permitido quando `OLLAMA_AGENT_AUTH_SSO_PUBLIC=true`; caso contrário o fluxo permanece vinculado a uma sessão autenticada. Os endpoints SAML usam variáveis `OLLAMA_AGENT_SAML_<PROVIDER>_*` para metadata, certificado, chave privada, ACS, EntityID e redirect. A ativação exige HTTPS e chaves/certificados fornecidos pelo operador. Para companions, `OLLAMA_AGENT_TLS_CERT_FILE`, `OLLAMA_AGENT_TLS_KEY_FILE` e, quando necessário, `OLLAMA_AGENT_TLS_CLIENT_CA_FILE` ativam TLS/mTLS no servidor.

## Provas executadas

- `gofmt -w internal/agent/*.go server/agent_routes.go`
- `go test ./internal/agent -count=1` — suíte agentic passou após correção do `Root` do builder.
- `go test ./server ./cmd/launch ./internal/multillm -count=1` — continua bloqueado neste sandbox pelo pacote upstream `mlx` sem arquivos compiláveis na plataforma; não é falha introduzida pelo adapter SAML.
- Testes de MFA, exportadores, canvas visual e contratos existentes estão no pacote `internal/agent`.
- Teste distribuído `distributed_integration_test.go` permanece com build tag `integration` e exige PostgreSQL, Redis e collector reais; não é substituto do gate unitário.

## Limites honestos

A geração de PDF/DOCX/PPTX é um exportador base de contrato, não ainda uma suíte editorial equivalente a PowerPoint, Word ou Typst. O OCR depende de Tesseract instalado. SAML está implementado como adapter e precisa de um IdP real para teste de ponta a ponta; push de produção, testes físicos de companions e publicação em lojas/clouds ainda exigem ambientes e credenciais do proprietário. O RLS possui teste distribuído real no CI, mas requer PostgreSQL para execução local. A síntese de harnesses orienta arquitetura, mas não permite copiar internals proprietários nem afirmar paridade de produto.
