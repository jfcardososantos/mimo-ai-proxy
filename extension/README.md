# Mimo Xiaomi Session Importer

Extensão Chrome/Edge para capturar a sessão autenticada da Xiaomi AI Studio e enviar ao Mimo Proxy.

## Instalação

1. Extraia este diretório em uma pasta local.
2. Abra `chrome://extensions` ou `edge://extensions`.
3. Ative `Developer mode`.
4. Clique em `Load unpacked`.
5. Selecione a pasta `extension`.

## Uso

1. Faça login em `https://aistudio.xiaomimimo.com/` no mesmo navegador.
2. Abra o popup da extensão.
3. Informe a URL pública do seu proxy.
4. Informe a `API_KEY` se o proxy estiver protegido.
5. Clique em `Import Xiaomi Session`.

Se funcionar, o proxy salva a sessão em `data/auth.json` e passa a usar os endpoints `/v1/*` com essa autenticação.
