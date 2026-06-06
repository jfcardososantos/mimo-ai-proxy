# flip-mimo-api

`flip-mimo-api` expõe os modelos da Xiaomi Mimo por uma API simples e compatível com OpenAI e Ollama.

O fluxo principal é:

1. fazer login no Xiaomi AI Studio;
2. importar a sessão com a extensão do Chrome/Edge;
3. chamar a API enviando apenas `model` e payload.

Os endpoints públicos não exigem token do cliente. A autenticação com a Xiaomi fica encapsulada no backend por meio do arquivo `data/auth.json`.

## O que o projeto entrega

- Compatibilidade com OpenAI em `POST /v1/chat/completions`, `POST /v1/completions` e `GET /v1/models`
- Compatibilidade com Ollama em `POST /api/chat`, `POST /api/generate`, `GET /api/tags` e `GET /api/version`
- Streaming
- Tool calling
- Persistência de histórico em SQLite
- Dashboard local para operação e teste
- Extensão para importar a sessão autenticada da Xiaomi

## Como funciona a autenticação

`flip-mimo-api` não depende mais de `SERVICE_TOKEN`, `USER_ID`, `XIAOMI_CHATBOT_PH` ou `XIAOMI_COOKIE` no ambiente para operar.

A sessão ativa é lida de:

- `data/auth.json`

Esse arquivo é preenchido pela extensão ou, se você quiser automatizar isso sem navegador, pelo endpoint administrativo `POST /auth/import`.

Se a Xiaomi invalidar a sessão:

1. `GET /auth/status` passa a indicar problema;
2. chamadas para `/v1/*` ou `/api/*` falham;
3. você reimporta a sessão pela extensão.

## Requisitos

- Go 1.24+ para execução local
- Docker e Docker Compose para execução em container
- Chrome ou Edge para usar a extensão

## Configuração

As variáveis de ambiente úteis agora são:

```env
PORT=3000
CORS_ORIGIN=*
API_KEY=
AUTH_STORE_PATH=
```

Notas:

- `API_KEY` é opcional.
- Se definida, ela protege apenas rotas administrativas de sessão, como `POST /auth/import`, `POST /auth/extension/import`, `POST /auth/clear` e `GET /auth/debug`.
- `AUTH_STORE_PATH` é opcional. Se não for definido, a sessão fica em `data/auth.json`.

## Executando

### Docker

```bash
docker-compose up -d
```

### Go

```bash
go mod tidy
go run main.go
```

Depois abra:

- `http://localhost:3000/`

## Setup recomendado com a extensão

### 1. Login na Xiaomi

Entre em:

- `https://aistudio.xiaomimimo.com/`

### 2. Baixe a extensão

Pela dashboard, baixe:

- `/downloads/mimo-xiaomi-session-extension.zip`

Ou use a pasta local `extension/`.

### 3. Instale no navegador

1. abra `chrome://extensions` ou `edge://extensions`
2. ative `Developer mode`
3. clique em `Load unpacked`
4. selecione a pasta `extension`

### 4. Importe a sessão

No popup da extensão:

1. informe a URL do proxy
2. informe a `API_KEY` apenas se você protegeu a rota administrativa
3. clique em `Import Xiaomi Session`

Se der certo, a sessão será salva em `data/auth.json`.

## Dashboard

A home em `/` foi desenhada para operação rápida:

- status da sessão
- QR code para abrir o Xiaomi AI Studio
- download da extensão
- exemplos de uso
- testador dos endpoints
- links para debug e limpeza de sessão

## Endpoints

### OpenAI

#### `POST /v1/chat/completions`

Compatível com o formato de chat da OpenAI.

```bash
curl http://localhost:3000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "mimo-v2.5-pro",
    "messages": [
      {"role": "user", "content": "Explique em uma frase o que este proxy faz."}
    ],
    "stream": false
  }'
```

#### `POST /v1/completions`

Endpoint legado. O proxy converte `prompt` internamente para chat.

#### `GET /v1/models`

Lista os modelos visíveis pela sessão autenticada na Xiaomi.

### Ollama

#### `POST /api/chat`

Se `stream` não for enviado, o proxy assume `false`.

```bash
curl http://localhost:3000/api/chat \
  -H "Content-Type: application/json" \
  -d '{
    "model": "mimo-v2.5-pro",
    "messages": [
      {"role": "user", "content": "Olá"}
    ]
  }'
```

#### `POST /api/generate`

Compatível com o formato `generate` do Ollama.

Se `stream` não for enviado, o proxy assume `false`.

#### `GET /api/tags`

Lista os modelos no formato Ollama.

#### `GET /api/version`

Expõe a versão compatível do adaptador Ollama.

### Operação e diagnóstico

#### `GET /health`

Retorna estado do processo e da sessão.

#### `GET /auth/status`

Mostra se a sessão carregada está configurada.

#### `GET /auth/debug`

Rota administrativa para inspecionar os campos persistidos. Se `API_KEY` estiver definida, precisa de autenticação.

#### `POST /auth/clear`

Remove a sessão salva.

#### `POST /auth/import`

Permite importar a sessão manualmente por payload, se você realmente precisar automatizar isso sem a extensão.

#### `POST /auth/extension/import`

Endpoint usado pela extensão.

## Integração com IDEs e clientes

### OpenAI-compatible

Configure o cliente para usar:

- Base URL: `http://localhost:3000/v1`

Não envie `Authorization` para as rotas públicas, a menos que seu cliente exija algum valor fictício. O backend não precisa dele para `/v1/*`.

### Ollama-compatible

Configure o cliente para usar:

- Base URL: `http://localhost:3000/api`

## Tool calling e agentes

O projeto converte ferramentas do formato OpenAI para o formato esperado pelo Mimo e devolve `tool_calls` compatíveis.

Recomendações:

- mantenha o `model` explícito
- envie `stream: true` apenas quando quiser resposta em streaming
- use `parallel_tool_calls: false` se o cliente tiver dificuldade com múltiplas tools por turno

## Persistência

Arquivos relevantes:

- `data/auth.json`: sessão da Xiaomi
- `data/history.db`: histórico local em SQLite

Se estiver em Docker, mantenha `data/` em volume persistente.

## Troubleshooting

### A API responde que a sessão não está configurada

Causa provável:

- a extensão ainda não importou a sessão
- `data/auth.json` foi apagado
- a sessão foi salva em outro caminho por `AUTH_STORE_PATH`

### A Xiaomi devolve 401

Causa provável:

- sessão expirada

Correção:

1. faça login de novo no Xiaomi AI Studio
2. reimporte a sessão pela extensão

### `GET /v1/models` não lista modelos

Verifique:

- se `GET /auth/status` mostra sessão válida
- se a conta usada no Xiaomi AI Studio ainda tem acesso aos modelos

## Extensão

Arquivos:

- `extension/manifest.json`
- `extension/popup.html`
- `extension/popup.js`

Resumo:

- lê cookies da Xiaomi no navegador
- monta a sessão
- envia para `POST /auth/extension/import`

## Licença

MIT
