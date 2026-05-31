# Mimo AI Gateway & Proxy (Go)

Gateway avançado e Proxy API de alta performance para o ecossistema Mimo AI da Xiaomi, projetado para fornecer uma ponte robusta entre as capacidades do Mimo e o padrão de mercado OpenAI.

## Visão Geral

O **Mimo AI Proxy** não é apenas uma camada de tradução; é um gateway completo que gerencia sessões, otimiza o uso de contexto, provê persistência de dados e oferece ferramentas de monitoramento em tempo real. Ele permite que desenvolvedores utilizem o Mimo AI como se fosse um modelo nativo da OpenAI, com suporte total a recursos avançados como streaming, reasoning e tool calling.

## Funcionalidades Principais

- **OpenAI Standard Gateway**: Implementação completa dos endpoints `/v1/chat/completions`, `/v1/completions` e `/v1/models`.
- **Inteligência de Sessão**: 
  - Detecção automática de conversas via fingerprinting de mensagens.
  - Sincronização bi-direcional com o histórico oficial da Xiaomi.
  - Persistência local robusta em SQLite.
- **Otimização de Contexto (Context Mastery)**:
  - Suporte a contextos massivos de até **1 Milhão de Tokens**.
  - Gerenciamento inteligente de payload, enviando apenas deltas quando uma sessão é identificada, garantindo estabilidade e performance.
- **AI-Native Features**:
  - **Reasoning (Thinking)**: Extração nativa de blocos `<think>` para o campo `reasoning_content`.
  - **Sequential Tool Calling**: Orquestração de múltiplas chamadas de ferramentas em sequência.
  - **Web Search**: Ativação dinâmica de busca na web via modelo, `web_search: true`, ferramentas com nome `search`/`web`, ou `DEFAULT_WEB_SEARCH=true`.
- **Infraestrutura e Operações**:
  - **Live Dashboard**: Interface web integrada para monitoramento de uptime, latência upstream e consumo de tokens por conta.
  - **Browser Extension Login Flow**: Extensão Chrome/Edge para capturar a sessão autenticada da Xiaomi AI Studio e salvar no proxy.
  - **Direct Proxy**: Acesso de baixo nível ao endpoint original da Xiaomi via `/open-apis/bot/chat`.

## Configuração

1. **Requisitos**: Go 1.24+ ou Docker.

2. **Variáveis de Ambiente**: Configure o `.env` (use `[.env.example](.env.example)` como base).
   ```env
   SERVICE_TOKEN="token"
   USER_ID="id"
   XIAOMI_CHATBOT_PH="ph"
   
   # Segurança e Rede:
   PORT=3000
   API_KEY="sua_chave_secreta"
   CORS_ORIGIN="*"
   ```

   Observações importantes:
   - Se você não quiser manter esses 3 valores manualmente, use a extensão do navegador para importar a sessão logada da Xiaomi.
   - O proxy também aceita `XIAOMI_COOKIE` bruto e salva sessões importadas em `data/auth.json`.
   - Os endpoints compatíveis com OpenAI continuam os mesmos, principalmente `POST /v1/chat/completions`.

## Como usar

### Docker (Recomendado)
```bash
docker-compose up -d
```

### Manualmente
```bash
go mod tidy
go run main.go
```

### Exemplo de Integração
```bash
curl http://localhost:3000/v1/chat/completions \
  -H "Authorization: Bearer sua_chave" \
  -d '{
    "model": "mimo-v2.5-pro",
    "messages": [{"role": "user", "content": "Explique a teoria da relatividade."}],
    "stream": true,
    "web_search": true
  }'
```

### IDE / Vibecoding (tool calls)

Configure o cliente OpenAI da IDE apontando para `http://localhost:3000/v1` com `Authorization: Bearer <API_KEY>`.

- Envie `tools` e `tool_choice` normalmente; o proxy converte para o formato XML do Mimo e devolve `tool_calls` compatíveis com OpenAI.
- Use `stream: true` para respostas em tempo real (recomendado para Cursor e similares).
- Para busca atualizada na web, use `"web_search": true` ou um modelo com `search` no nome.
- Com `parallel_tool_calls: false`, apenas a primeira ferramenta é retornada por turno; as demais seguem após mensagens `role: tool`.

### Kilo Code (agent para após reasoning)

Se o agente planeja (“vou ler o projeto…”) e para sem executar tools:

1. Com tools, o proxy já desliga thinking por padrão (use `AGENT_ENABLE_THINKING=true` só se quiser reasoning de volta).
2. Use `stream: true` no provider OpenAI do Kilo.
3. Confirme que o modelo no Kilo é o mesmo configurado no proxy (ex. `mimo-v2.5-pro`).

O proxy agora preserva `reasoning_content` no histórico (exigido pelo Mimo em multi-turn com tools) e corrige o parse das tags `<think>`.

## Setup Assistido

Ao abrir `/`, o proxy mostra:
- status atual da autenticação;
- QR code para abrir `https://aistudio.xiaomimimo.com/`;
- download da extensão para importar a sessão autenticada da Xiaomi;
- formulário alternativo para salvar `XIAOMI_COOKIE` bruto ou os campos `serviceToken`, `userId` e `xiaomichatbot_ph`.

As credenciais salvas pela interface ficam em `data/auth.json` por padrão.

Fluxo recomendado:
1. Faça login no Xiaomi AI Studio.
2. Instale a extensão disponível no dashboard.
3. Informe a URL do proxy e a `API_KEY`, se existir.
4. Clique em `Import Xiaomi Session`.
5. Use normalmente `/v1/chat/completions`, `/v1/models` e os demais endpoints.

## Arquitetura de Dados

O gateway utiliza uma base **SQLite** local (`data/history.db`) para garantir que as conversas sejam mantidas mesmo entre reinicializações, permitindo consultas rápidas ao histórico e sincronização sob demanda com a nuvem da Xiaomi.

## Licença

MIT
