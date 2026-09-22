# DZ23 Agentic Mobile

Cliente Expo para acompanhar missões, visualizar timeline, aprovar/rejeitar passos protegidos, iniciar execução e consultar uma API agentic v1 autenticada.

## Desenvolvimento

```bash
npm install
npm run start
```

No emulador ou dispositivo físico, informe em **Servidor** uma URL alcançável pelo celular, por exemplo `http://192.168.0.10:11434`. O cliente persiste a URL no AsyncStorage e o token Bearer no SecureStore nativo. O servidor continua sendo a autoridade de policy.

## Autenticação

Em modo multiusuário, forneça um token Bearer emitido pelo servidor. Para desenvolvimento local, o servidor pode habilitar explicitamente `OLLAMA_AGENT_AUTH_REQUIRED=true` e `OLLAMA_AGENT_AUTH_DEV=true`, emitir um token pelo endpoint de bootstrap e depois desabilitar o bootstrap de desenvolvimento. Não coloque tokens em `.env`, bundle, screenshots ou repositório.

## Android e iOS

Os identificadores de produção são `com.lmprado.dz23agentic`. Para builds EAS, configure primeiro a conta Expo/EAS e as credenciais de assinatura fora do repositório:

```bash
npm install -g eas-cli
eas login
eas build:configure
npm run build:android
npm run build:ios
npm run build:all
```

O arquivo `eas.json` possui perfis `development`, `preview` e `production`. O envio para lojas exige preencher o `ascAppId` do App Store Connect e configurar credenciais reais na conta EAS; esta etapa não é simulada pelo código.

O mobile não executa shell, browser, processos ou conectores diretamente. Ele solicita operações pela API, observa eventos e apresenta approvals. Polling periódico é usado como fallback compatível com Android/iOS; push notifications podem ser adicionadas com um provedor de notificações configurado por organização.
