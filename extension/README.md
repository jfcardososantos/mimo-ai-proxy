# flip-mimo-api Session Importer

Extensão Chrome/Edge para capturar a sessão autenticada da Xiaomi AI Studio e enviar ao `flip-mimo-api`.

## Instalação

1. extraia este diretório em uma pasta local
2. abra `chrome://extensions` ou `edge://extensions`
3. ative `Developer mode`
4. clique em `Load unpacked`
5. selecione a pasta `extension`

## Uso

1. faça login em `https://aistudio.xiaomimimo.com/` no mesmo navegador
2. abra o popup da extensão
3. informe a URL pública do seu `flip-mimo-api`
4. se você protegeu `POST /auth/extension/import`, informe a `API_KEY`
5. clique em `Import Xiaomi Session`

Se funcionar, a sessão será salva em `data/auth.json` e o backend passará a autenticar internamente as chamadas para a Xiaomi.
