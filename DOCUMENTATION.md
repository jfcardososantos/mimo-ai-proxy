# Documentação da API — flip-mimo-api

Proxy que expõe os modelos da Xiaomi Mimo via API compatível com OpenAI e Ollama.

---

## Índice

1. [Autenticação do Cliente](#1-autenticação-do-cliente)
2. [Headers Padrão](#2-headers-padrão)
3. [Modelos Disponíveis](#3-modelos-disponíveis)
4. [OpenAI-Compatible API](#4-openai-compatible-api)
   - [`GET /v1/models`](#get-v1models)
   - [`POST /v1/chat/completions`](#post-v1chatcompletions)
   - [`POST /v1/completions` (legado)](#post-v1completions-legado)
   - [`GET /v1/chat/history/:conversationId`](#get-v1chathistoryconversationid)
5. [Ollama-Compatible API](#5-ollama-compatible-api)
   - [`GET /api/tags`](#get-apitags)
   - [`POST /api/chat`](#post-apichat)
   - [`POST /api/generate`](#post-apigenerate)
   - [`GET /api/version`](#get-apiversion)
6. [Agent API](#6-agent-api)
   - [`POST /v1/agent/run`](#post-v1agentrun)
   - [`GET /v1/agent/status/:id`](#get-v1agentstatusid)
   - [`GET /v1/agent/stream/:id`](#get-v1agentstreamid)
7. [Proxy Direto](#7-proxy-direto)
   - [`POST /open-apis/bot/chat`](#post-open-apisbotchat)
8. [Admin / Setup](#8-admin--setup)
   - [`GET /health`](#get-health)
   - [`GET /auth/status`](#get-authstatus)
   - [`GET /auth/debug`](#get-authdebug)
   - [`POST /auth/import`](#post-authimport)
   - [`POST /auth/clear`](#post-authclear)
   - [`POST /auth/extension/import`](#post-authextensionimport)
9. [Tool Calling / Agentes](#9-tool-calling--agentes)
10. [Upload de Mídia](#10-upload-de-mídia)
11. [Variáveis de Ambiente](#11-variáveis-de-ambiente)
12. [Exemplos Rápidos](#12-exemplos-rápidos)

---

## 1. Autenticação do Cliente

### Rotas Públicas (não exigem token)

Os endpoints `/v1/*` e `/api/*` são **públicos** — nenhum `Authorization` é necessário. O proxy cuida da autenticação com a Xiaomi internamente via `data/auth.json`.

### Rotas Administrativas (protegidas por API_KEY)

Se a variável `API_KEY` estiver definida no ambiente, as rotas administrativas (`POST /auth/import`, `POST /auth/clear`, `GET /auth/debug`) exigem:

```
Authorization: Bearer <API_KEY>
```

---

## 2. Headers Padrão

### Headers que você envia

| Header | Obrigatório | Descrição |
|--------|-------------|-----------|
| `Content-Type` | ✅ | `application/json` |
| `Authorization` | ❌ | `Bearer <API_KEY>` (só para rotas admin) |
| `X-Timezone` | ❌ | Fuso horário (ex: `America/Sao_Paulo`) |
| `Accept` | ❌ | `application/json` (padrão) |

### Headers que o proxy envia para a Xiaomi

O proxy monta estes headers automaticamente (com base na sessão):

```
accept: */*
accept-language: system
content-type: application/json
cookie: serviceToken="..."; userId=...; xiaomichatbot_ph="..."
origin: https://aistudio.xiaomimimo.com
referer: https://aistudio.xiaomimimo.com/
user-agent: Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 ...
x-timezone: America/Maceio
```

---

## 3. Modelos Disponíveis

### Modelo principal

| Modelo | Descrição |
|--------|-----------|
| `mimo-v2.5-pro` | Modelo padrão, com suporte a thinking |

### Variações

| Sufixo no nome | Efeito |
|----------------|--------|
| `-no-thinking` | Desativa o raciocínio interno (thinking) |
| Ex: `mimo-v2.5-pro-no-thinking` | |

> A lista completa de modelos disponíveis para sua sessão pode ser obtida via `GET /v1/models` ou `GET /api/tags`.

---

## 4. OpenAI-Compatible API

Base URL: `http://localhost:3000/v1`

---

### `GET /v1/models`

Lista todos os modelos disponíveis na sessão Xiaomi.

```bash
curl -s http://localhost:3000/v1/models | jq
```

**Exemplo de resposta:**

```json
{
  "object": "list",
  "data": [
    {
      "id": "mimo-v2.5-pro",
      "object": "model",
      "created": 1700000000,
      "owned_by": "xiaomi",
      "description": "Mimo 2.5 Pro"
    }
  ]
}
```

---

### `POST /v1/chat/completions`

Endpoint principal de chat. Compatível com o formato OpenAI.

#### Chat simples (sem streaming)

```bash
curl -s http://localhost:3000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "mimo-v2.5-pro",
    "messages": [
      {"role": "system", "content": "Você é um assistente útil."},
      {"role": "user", "content": "Explique o que é um proxy de IA em uma frase."}
    ],
    "stream": false
  }' | jq
```

#### Chat com streaming (SSE)

```bash
curl -s http://localhost:3000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "mimo-v2.5-pro",
    "messages": [
      {"role": "user", "content": "Conte uma história curta."}
    ],
    "stream": true
  }'
```

#### Com web search ativado

```bash
curl -s http://localhost:3000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "mimo-v2.5-pro",
    "messages": [
      {"role": "user", "content": "Qual a cotação do dólar hoje?"}
    ],
    "stream": false,
    "web_search": true
  }' | jq
```

#### Com tool calling (funções)

```bash
curl -s http://localhost:3000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "mimo-v2.5-pro",
    "messages": [
      {"role": "user", "content": "Qual a previsão do tempo para São Paulo hoje?"}
    ],
    "tools": [
      {
        "type": "function",
        "function": {
          "name": "get_weather",
          "description": "Obtém a previsão do tempo para uma cidade",
          "parameters": {
            "type": "object",
            "properties": {
              "city": {
                "type": "string",
                "description": "Nome da cidade"
              }
            },
            "required": ["city"]
          }
        }
      }
    ],
    "tool_choice": "auto",
    "parallel_tool_calls": false
  }' | jq
```

#### Com session ID (para manter histórico)

O campo `user` atua como **identificador de sessão**. Use o mesmo valor para manter o contexto entre turnos.

```bash
# Primeira mensagem
curl -s http://localhost:3000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "mimo-v2.5-pro",
    "messages": [
      {"role": "user", "content": "Meu nome é João."}
    ],
    "user": "sessao_joao"
  }' | jq

# Segunda mensagem (mesmo user -> mantém contexto)
curl -s http://localhost:3000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "mimo-v2.5-pro",
    "messages": [
      {"role": "user", "content": "Qual é o meu nome?"}
    ],
    "user": "sessao_joao"
  }' | jq
```

#### Desativando thinking (raciocínio interno)

```bash
curl -s http://localhost:3000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "mimo-v2.5-pro-no-thinking",
    "messages": [
      {"role": "user", "content": "O que é Golang?"}
    ],
    "stream": false
  }' | jq
```

**Parâmetros do body:**

| Campo | Tipo | Obrigatório | Padrão | Descrição |
|-------|------|-------------|--------|-----------|
| `model` | string | ❌ | `mimo-v2.5-pro` | Nome do modelo |
| `messages` | array | ✅ | — | Lista de mensagens |
| `stream` | bool | ❌ | `false` | Habilita SSE streaming |
| `user` | string | ❌ | auto | Identificador de sessão |
| `tools` | array | ❌ | — | Definições de ferramentas |
| `tool_choice` | string | ❌ | `auto` | `auto`, `none`, `required`, ou nome da tool |
| `parallel_tool_calls` | bool | ❌ | `true` | Se `false`, executa 1 tool por vez |
| `web_search` | bool | ❌ | `false` | Ativa busca na web |

---

### `POST /v1/completions` (legado)

Endpoint legado. O proxy converte `prompt` internamente para o formato chat.

```bash
curl -s http://localhost:3000/v1/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "mimo-v2.5-pro",
    "prompt": "Complete esta frase: O Brasil é um país",
    "max_tokens": 100,
    "temperature": 0.7
  }' | jq
```

---

### `GET /v1/chat/history/:conversationId`

Recupera o histórico de uma conversa específica.

```bash
# Histórico local
curl -s http://localhost:3000/v1/chat/history/ID_DA_CONVERSA | jq

# Com sync (força busca na Xiaomi)
curl -s "http://localhost:3000/v1/chat/history/ID_DA_CONVERSA?sync=true" | jq
```

---

## 5. Ollama-Compatible API

Base URL: `http://localhost:3000/api`

---

### `GET /api/tags`

Lista os modelos no formato Ollama.

```bash
curl -s http://localhost:3000/api/tags | jq
```

**Exemplo de resposta:**

```json
{
  "models": [
    {
      "name": "mimo-v2.5-pro",
      "model": "mimo-v2.5-pro",
      "modified_at": "2026-05-02T...",
      "size": 0,
      "digest": "",
      "details": {
        "format": "xiaomi",
        "family": "mimo",
        "families": ["mimo"],
        "parameter_size": "",
        "quantization_level": ""
      }
    }
  ]
}
```

---

### `POST /api/chat`

Formato compatível com Ollama Chat.

```bash
curl -s http://localhost:3000/api/chat \
  -H "Content-Type: application/json" \
  -d '{
    "model": "mimo-v2.5-pro",
    "messages": [
      {"role": "user", "content": "O que é o Golang?"}
    ]
  }' | jq
```

#### Com streaming

```bash
curl -s http://localhost:3000/api/chat \
  -H "Content-Type: application/json" \
  -d '{
    "model": "mimo-v2.5-pro",
    "messages": [
      {"role": "user", "content": "Fale sobre inteligência artificial."}
    ],
    "stream": true
  }'
```

#### Com thinking habilitado

```bash
curl -s http://localhost:3000/api/chat \
  -H "Content-Type: application/json" \
  -d '{
    "model": "mimo-v2.5-pro",
    "messages": [
      {"role": "user", "content": "Resolva: 2+2"}
    ],
    "think": true
  }' | jq
```

#### Com tool calling

```bash
curl -s http://localhost:3000/api/chat \
  -H "Content-Type: application/json" \
  -d '{
    "model": "mimo-v2.5-pro",
    "messages": [
      {"role": "user", "content": "Busque informações sobre a linguagem Rust."}
    ],
    "tools": [
      {
        "type": "function",
        "function": {
          "name": "web_search",
          "description": "Busca informações na web",
          "parameters": {
            "type": "object",
            "properties": {
              "query": {
                "type": "string",
                "description": "Termo de busca"
              }
            },
            "required": ["query"]
          }
        }
      }
    ]
  }' | jq
```

---

### `POST /api/generate`

Formato compatível com `generate` do Ollama.

```bash
curl -s http://localhost:3000/api/generate \
  -H "Content-Type: application/json" \
  -d '{
    "model": "mimo-v2.5-pro",
    "prompt": "Explique o que é Kubernetes",
    "system": "Você é um especialista em DevOps.",
    "stream": false
  }' | jq
```

#### Com streaming

```bash
curl -s http://localhost:3000/api/generate \
  -H "Content-Type: application/json" \
  -d '{
    "model": "mimo-v2.5-pro",
    "prompt": "Escreva um poema sobre programação.",
    "stream": true
  }'
```

---

### `GET /api/version`

```bash
curl -s http://localhost:3000/api/version | jq
```

**Resposta:** `{"version": "0.0.0-mimo-proxy"}`

---

## 6. Agent API

Endpoints para executar agentes autônomos (loop planejador → executor → crítico).

---

### `POST /v1/agent/run`

Inicia um loop de agente para cumprir um objetivo.

```bash
curl -s http://localhost:3000/v1/agent/run \
  -H "Content-Type: application/json" \
  -d '{
    "goal": "Crie um arquivo hello.py com um print('Hello World')",
    "max_steps": 10
  }' | jq
```

**Parâmetros:**

| Campo | Tipo | Obrigatório | Padrão | Descrição |
|-------|------|-------------|--------|-----------|
| `goal` | string | ✅ | — | Objetivo a ser cumprido |
| `goal_id` | string | ❌ | auto gerado | ID para acompanhamento |
| `max_steps` | number | ❌ | `10` | Número máximo de passos |

**Resposta (202 Accepted):**

```json
{
  "message": "Agent loop started",
  "goal_id": "goal_abc123..."
}
```

---

### `GET /v1/agent/status/:id`

Consulta o estado atual de um agente.

```bash
curl -s http://localhost:3000/v1/agent/status/goal_abc123... | jq
```

---

### `GET /v1/agent/stream/:id`

Acompanha o agente em tempo real via SSE (Server-Sent Events).

```bash
curl -s http://localhost:3000/v1/agent/stream/goal_abc123...
```

O streaming emite eventos como:

```
data: {"type":"connected"}
data: {"type":"planning","task":"..."}
data: {"type":"executing","action":"..."}
data: {"type":"finished","output":"..."}
```

---

## 7. Proxy Direto

### `POST /open-apis/bot/chat`

Proxy direto para a API da Xiaomi, sem tradução de formato.

```bash
curl -s http://localhost:3000/open-apis/bot/chat \
  -H "Content-Type: application/json" \
  -d '{
    "msgId": "msg_123",
    "conversationId": "conv_456",
    "query": "Olá, tudo bem?",
    "isEditedQuery": false,
    "modelConfig": {
      "enableThinking": true,
      "webSearchStatus": "disabled",
      "model": "mimo-v2.5-pro"
    },
    "multiMedias": []
  }' | jq
```

---

## 8. Admin / Setup

### `GET /health`

```bash
curl -s http://localhost:3000/health | jq
```

**Resposta:**

```json
{
  "status": "ok",
  "uptime": 1234.56,
  "authStatus": "ok",
  "authError": ""
}
```

---

### `GET /auth/status`

Verifica se a sessão Xiaomi está configurada.

```bash
curl -s http://localhost:3000/auth/status | jq
```

**Resposta:**

```json
{
  "configured": true,
  "authError": "",
  "authSource": "data/auth.json",
  "selectedPh": "ph_abc...",
  "storePath": "data/auth.json"
}
```

---

### `GET /auth/debug`

Inspeciona os campos da autenticação (requer `API_KEY` se configurada).

```bash
curl -s http://localhost:3000/auth/debug \
  -H "Authorization: Bearer <API_KEY>" | jq
```

---

### `POST /auth/import`

Importa a sessão manualmente (requer `API_KEY` se configurada).

```bash
curl -s http://localhost:3000/auth/import \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <API_KEY>" \
  -d '{
    "service_token": "token_aqui",
    "user_id": "user_id_aqui",
    "xiaomi_chatbot_ph": "ph_aqui",
    "xiaomi_cookie": "cookie_completo_aqui"
  }' | jq
```

> **Dica:** Se você já tem os cookies da sessão, envie apenas `xiaomi_cookie` com o valor completo. O proxy extrai automaticamente `serviceToken`, `userId` e `xiaomichatbot_ph`.

---

### `POST /auth/clear`

Remove a sessão salva (requer `API_KEY` se configurada).

```bash
curl -s -X POST http://localhost:3000/auth/clear \
  -H "Authorization: Bearer <API_KEY>" | jq
```

**Resposta:** `{"cleared": true}`

---

### `POST /auth/extension/import`

Endpoint usado pela extensão do Chrome. Aceita o mesmo payload que `POST /auth/import`, mas em formato específico da extensão.

```bash
curl -s http://localhost:3000/auth/extension/import \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <API_KEY>" \
  -d '{
    "serviceToken": "token_aqui",
    "userId": "user_id_aqui",
    "xiaomichatbotPh": "ph_aqui",
    "rawCookie": "cookie_completo_aqui",
    "source": "chrome-extension"
  }' | jq
```

---

## 9. Tool Calling / Agentes

### Formato OpenAI

O formato segue o padrão OpenAI. Exemplo de resposta com `tool_calls`:

```json
{
  "id": "chatcmpl-abc...",
  "object": "chat.completion",
  "choices": [{
    "index": 0,
    "message": {
      "role": "assistant",
      "content": null,
      "tool_calls": [{
        "id": "call_abc...",
        "type": "function",
        "function": {
          "name": "get_weather",
          "arguments": "{\"city\": \"São Paulo\"}"
        }
      }]
    },
    "finish_reason": "tool_calls"
  }]
}
```

### Fluxo de tool calling

1. Você envia `messages` + `tools`
2. O modelo responde com `finish_reason: "tool_calls"` e `tool_calls`
3. Você executa a ferramenta e envia o resultado como `{"role": "tool", "tool_call_id": "...", "content": "..."}`
4. O modelo continua com a resposta final

### Exemplo completo (curl com tool calling em 2 turnos)

```bash
# Turno 1: modelo decide chamar a ferramenta
curl -s http://localhost:3000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "mimo-v2.5-pro",
    "messages": [
      {"role": "user", "content": "Qual a previsão do tempo em São Paulo?"}
    ],
    "tools": [{
      "type": "function",
      "function": {
        "name": "get_weather",
        "description": "Previsão do tempo",
        "parameters": {
          "type": "object",
          "properties": {
            "city": {"type": "string"}
          },
          "required": ["city"]
        }
      }
    }]
  }' | jq '.choices[0].message'
```

```bash
# Turno 2: enviamos o resultado da ferramenta
curl -s http://localhost:3000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "mimo-v2.5-pro",
    "messages": [
      {"role": "user", "content": "Qual a previsão do tempo em São Paulo?"},
      {"role": "assistant", "tool_calls": [{"id": "call_abc", "type": "function", "function": {"name": "get_weather", "arguments": "{\"city\": \"São Paulo\"}"}}]},
      {"role": "tool", "tool_call_id": "call_abc", "content": "23°C, parcialmente nublado"}
    ],
    "tools": [{
      "type": "function",
      "function": {
        "name": "get_weather",
        "description": "Previsão do tempo",
        "parameters": {
          "type": "object",
          "properties": {
            "city": {"type": "string"}
          },
          "required": ["city"]
        }
      }
    }]
  }' | jq '.choices[0].message.content'
```

### parallel_tool_calls

- **`true`** (padrão): o modelo pode chamar múltiplas ferramentas em paralelo
- **`false`**: o modelo chama uma ferramenta por vez (modo sequencial)

> ⚠️ Com `parallel_tool_calls: false`, você precisa enviar o resultado da tool, e o proxy automaticamente enfileira a próxima chamada.

---

## 10. Upload de Mídia

O proxy suporta upload de imagens para a Xiaomi. O processo é:

1. Gera informações de upload via API Xiaomi
2. Faz upload do arquivo para o servidor FDS
3. Parseia o recurso
4. Retorna um objeto `MultiMedia` para usar nas requisições de chat

> ⚠️ O upload de mídia é feito internamente pelo serviço `UploadToXiaomi()`. Não há um endpoint REST exposto diretamente, mas o fluxo de chat com imagens é suportado através da API da Xiaomi.

---

## 11. Variáveis de Ambiente

| Variável | Obrigatório | Padrão | Descrição |
|----------|-------------|--------|-----------|
| `PORT` | ❌ | `3000` | Porta do servidor |
| `CORS_ORIGIN` | ❌ | `*` | Origem permitida no CORS |
| `API_KEY` | ❌ | — | Chave para proteger rotas admin |
| `AUTH_STORE_PATH` | ❌ | `data/auth.json` | Caminho do arquivo de sessão |
| `DEFAULT_WEB_SEARCH` | ❌ | — | Se `true`, ativa web search em todas as chamadas |
| `AGENT_ENABLE_THINKING` | ❌ | — | Se `true`, mantém thinking ativo mesmo com tools |

---

## 12. Exemplos Rápidos

### Testar se o servidor está rodando

```bash
curl http://localhost:3000/health
```

### Chat mais simples possível

```bash
curl http://localhost:3000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"mimo-v2.5-pro","messages":[{"role":"user","content":"Olá"}]}'
```

### Streaming para terminal

```bash
curl -N http://localhost:3000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"mimo-v2.5-pro","messages":[{"role":"user","content":"Escreva um texto longo sobre IA"}],"stream":true}'
```

### Listar modelos

```bash
curl http://localhost:3000/v1/models | jq '.data[].id'
```

### Verificar se a sessão Xiaomi está ativa

```bash
curl http://localhost:3000/auth/status | jq '.configured'
```

---

## Integração com Clientes

### OpenAI Python Client

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:3000/v1",
    api_key="ignored"  # O proxy não exige key para /v1/*
)

response = client.chat.completions.create(
    model="mimo-v2.5-pro",
    messages=[{"role": "user", "content": "O que é Go?"}]
)
print(response.choices[0].message.content)
```

### Ollama Python Client

```python
import requests

response = requests.post("http://localhost:3000/api/chat", json={
    "model": "mimo-v2.5-pro",
    "messages": [{"role": "user", "content": "O que é Go?"}]
})
print(response.json()["message"]["content"])
```

### cURL com várias mensagens (com system prompt)

```bash
curl -s http://localhost:3000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "mimo-v2.5-pro",
    "messages": [
      {"role": "system", "content": "Responda sempre em português e de forma concisa."},
      {"role": "user", "content": "O que é Docker?"},
      {"role": "assistant", "content": "Docker é uma plataforma de containers."},
      {"role": "user", "content": "Qual a diferença para uma VM?"}
    ],
    "stream": false
  }' | jq '.choices[0].message.content'
```

---

> **Dica:** Para formatar a saída JSON com `jq`, instale com `brew install jq` (macOS) ou `apt install jq` (Linux).
