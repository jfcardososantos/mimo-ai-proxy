const proxyUrlInput = document.getElementById("proxyUrl");
const apiKeyInput = document.getElementById("apiKey");
const statusOutput = document.getElementById("status");
const saveConfigButton = document.getElementById("saveConfig");
const importButton = document.getElementById("importSession");
const importDeepSeekButton = document.getElementById("importDeepSeekSession");
const importKimiButton = document.getElementById("importKimiSession");
const openXiaomiButton = document.getElementById("openXiaomi");
const openDeepSeekButton = document.getElementById("openDeepSeek");
const openKimiButton = document.getElementById("openKimi");
const geminiApiKeyInput = document.getElementById("geminiApiKey");
const groqApiKeyInput = document.getElementById("groqApiKey");
const openRouterApiKeyInput = document.getElementById("openRouterApiKey");
const openRouterRefererInput = document.getElementById("openRouterReferer");
const openRouterTitleInput = document.getElementById("openRouterTitle");
const cloudflareApiKeyInput = document.getElementById("cloudflareApiKey");
const cloudflareAccountIdInput = document.getElementById("cloudflareAccountId");
const openGeminiButton = document.getElementById("openGemini");
const openGroqButton = document.getElementById("openGroq");
const openOpenRouterButton = document.getElementById("openOpenRouter");
const openCloudflareButton = document.getElementById("openCloudflare");
const saveGeminiButton = document.getElementById("saveGemini");
const saveGroqButton = document.getElementById("saveGroq");
const saveOpenRouterButton = document.getElementById("saveOpenRouter");
const saveCloudflareButton = document.getElementById("saveCloudflare");

const XIAOMI_STUDIO_URL = "https://aistudio.xiaomimimo.com/";
const DEEPSEEK_CHAT_URL = "https://chat.deepseek.com/";
const KIMI_CHAT_URL = "https://www.kimi.com/";
const GEMINI_KEYS_URL = "https://aistudio.google.com/app/apikey";
const GROQ_KEYS_URL = "https://console.groq.com/keys";
const OPENROUTER_KEYS_URL = "https://openrouter.ai/settings/keys";
const CLOUDFLARE_KEYS_URL = "https://dash.cloudflare.com/profile/api-tokens";
const COOKIE_NAMES = ["serviceToken", "userId", "xiaomichatbot_ph"];
const COOKIE_URLS = [
  "https://aistudio.xiaomimimo.com/",
  "https://xiaomimimo.com/",
  "https://account.xiaomi.com/"
];
const DEEPSEEK_COOKIE_URLS = [
  "https://chat.deepseek.com/",
  "https://deepseek.com/"
];
const KIMI_COOKIE_URLS = ["https://www.kimi.com/", "https://kimi.com/"];

function setStatus(message) {
  statusOutput.value = message;
}

function normalizeProxyUrl(url) {
  return url.trim().replace(/\/+$/, "");
}

async function loadConfig() {
  const stored = await chrome.storage.local.get(["proxyUrl", "apiKey", "openRouterReferer", "openRouterTitle"]);
  proxyUrlInput.value = stored.proxyUrl || "";
  apiKeyInput.value = stored.apiKey || "";
  openRouterRefererInput.value = stored.openRouterReferer || "";
  openRouterTitleInput.value = stored.openRouterTitle || "flip-ai";
  setStatus("Configure a URL do flip-ai e importe a sessão do provedor desejado. A API principal não exige token.");
}

async function saveConfig() {
  const proxyUrl = normalizeProxyUrl(proxyUrlInput.value);
  const apiKey = apiKeyInput.value.trim();

  await chrome.storage.local.set({ proxyUrl, apiKey });
  setStatus("Configuração salva.");
}

async function getCookieByName(name) {
  for (const url of COOKIE_URLS) {
    const cookie = await chrome.cookies.get({ url, name });
    if (cookie && cookie.value) {
      return cookie;
    }
  }

  const fallbackCookies = await chrome.cookies.getAll({ name });
  return fallbackCookies.find((cookie) => cookie.domain.includes("xiaomi")) || null;
}

async function collectRawCookieJar() {
  const seen = new Map();

  for (const url of COOKIE_URLS) {
    const cookies = await chrome.cookies.getAll({ url });
    for (const cookie of cookies) {
      if (!cookie || !cookie.name) {
        continue;
      }
      seen.set(cookie.name, cookie.value);
    }
  }

  const fallbackCookies = await chrome.cookies.getAll({});
  for (const cookie of fallbackCookies) {
    if (!cookie || !cookie.name) {
      continue;
    }
    if (!cookie.domain.includes("xiaomi") && !cookie.domain.includes("xiaomimimo")) {
      continue;
    }
    if (!seen.has(cookie.name)) {
      seen.set(cookie.name, cookie.value);
    }
  }

  return Array.from(seen.entries())
    .map(([name, value]) => `${name}=${value}`)
    .join("; ");
}

async function collectDeepSeekRawCookieJar() {
  const seen = new Map();

  for (const url of DEEPSEEK_COOKIE_URLS) {
    const cookies = await chrome.cookies.getAll({ url });
    for (const cookie of cookies) {
      if (cookie && cookie.name) {
        seen.set(cookie.name, cookie.value);
      }
    }
  }

  const fallbackCookies = await chrome.cookies.getAll({});
  for (const cookie of fallbackCookies) {
    if (!cookie || !cookie.name || !cookie.domain.includes("deepseek")) {
      continue;
    }
    if (!seen.has(cookie.name)) {
      seen.set(cookie.name, cookie.value);
    }
  }

  return Array.from(seen.entries())
    .map(([name, value]) => `${name}=${value}`)
    .join("; ");
}

async function collectKimiRawCookieJar() {
  const seen = new Map();
  for (const url of KIMI_COOKIE_URLS) {
    for (const cookie of await chrome.cookies.getAll({ url })) {
      if (cookie && cookie.name) seen.set(cookie.name, cookie.value);
    }
  }
  return Array.from(seen.entries()).map(([name, value]) => `${name}=${value}`).join("; ");
}

async function getKimiAccessToken() {
  const tabs = await chrome.tabs.query({ url: "https://www.kimi.com/*" });
  const tab = tabs.find((item) => item.id);
  if (!tab) throw new Error("Abra https://www.kimi.com logado antes de importar.");
  const [result] = await chrome.scripting.executeScript({
    target: { tabId: tab.id },
    func: () => localStorage.getItem("access_token") || ""
  });
  const token = String(result && result.result || "").trim();
  if (!token) throw new Error("Não encontrei localStorage.access_token na aba do Kimi.");
  return token;
}

function normalizeDeepSeekToken(raw) {
  if (!raw) {
    return "";
  }
  try {
    const parsed = JSON.parse(raw);
    if (parsed && typeof parsed.value === "string") {
      return parsed.value;
    }
  } catch (error) {
    // Keep raw value when it is not JSON.
  }
  return String(raw).trim();
}

async function getDeepSeekUserToken() {
  const tabs = await chrome.tabs.query({ url: "https://chat.deepseek.com/*" });
  const tab = tabs.find((item) => item.id);
  if (!tab) {
    throw new Error("Abra https://chat.deepseek.com logado antes de importar.");
  }

  const [result] = await chrome.scripting.executeScript({
    target: { tabId: tab.id },
    func: () => localStorage.getItem("userToken") || ""
  });

  const token = normalizeDeepSeekToken(result && result.result);
  if (!token) {
    throw new Error("Não encontrei localStorage.userToken na aba do DeepSeek.");
  }
  return token;
}

async function collectSession() {
  const found = {};

  for (const name of COOKIE_NAMES) {
    const cookie = await getCookieByName(name);
    if (!cookie || !cookie.value) {
      throw new Error(`Missing Xiaomi cookie: ${name}`);
    }
    found[name] = cookie.value;
  }

  const rawCookie = await collectRawCookieJar();
  if (!rawCookie) {
    throw new Error("Could not build Xiaomi raw cookie jar");
  }

  return {
    serviceToken: found.serviceToken,
    userId: found.userId,
    xiaomichatbotPh: found.xiaomichatbot_ph,
    rawCookie
  };
}

async function importSession() {
  const proxyUrl = normalizeProxyUrl(proxyUrlInput.value);
  const apiKey = apiKeyInput.value.trim();

  if (!proxyUrl) {
    setStatus("Informe a URL do proxy antes de importar.");
    return;
  }

  setStatus("Lendo cookies da Xiaomi...");

  try {
    const session = await collectSession();
    setStatus("Cookies encontrados. Enviando sessão ao proxy...");

    const headers = {
      "Content-Type": "application/json"
    };
    if (apiKey) {
      headers.Authorization = `Bearer ${apiKey}`;
    }

    const response = await fetch(`${proxyUrl}/auth/extension/import`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        ...session,
        source: "chrome-extension"
      })
    });

    const bodyText = await response.text();
    let prettyBody = bodyText;
    try {
      prettyBody = JSON.stringify(JSON.parse(bodyText), null, 2);
    } catch (error) {
      // Keep original text when response is not JSON.
    }

    setStatus(`HTTP ${response.status} ${response.statusText}\n\n${prettyBody}`);
  } catch (error) {
    setStatus(`Falha ao importar a sessão.\n\n${error.message || String(error)}`);
  }
}

async function importDeepSeekSession() {
  const proxyUrl = normalizeProxyUrl(proxyUrlInput.value);
  const apiKey = apiKeyInput.value.trim();

  if (!proxyUrl) {
    setStatus("Informe a URL do proxy antes de importar.");
    return;
  }

  setStatus("Lendo cookies e userToken do DeepSeek...");

  try {
    const [rawCookie, userToken] = await Promise.all([
      collectDeepSeekRawCookieJar(),
      getDeepSeekUserToken()
    ]);

    if (!rawCookie) {
      throw new Error("Could not build DeepSeek raw cookie jar");
    }

    setStatus("Credenciais DeepSeek encontradas. Enviando sessão ao proxy...");

    const headers = {
      "Content-Type": "application/json"
    };
    if (apiKey) {
      headers.Authorization = `Bearer ${apiKey}`;
    }

    const response = await fetch(`${proxyUrl}/auth/web/import`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        provider: "deepseek",
        token: userToken,
        rawCookie,
        user_agent: navigator.userAgent,
        storage: {
          userToken
        },
        source: "chrome-extension"
      })
    });

    const bodyText = await response.text();
    let prettyBody = bodyText;
    try {
      prettyBody = JSON.stringify(JSON.parse(bodyText), null, 2);
    } catch (error) {
      // Keep original text when response is not JSON.
    }

    setStatus(`HTTP ${response.status} ${response.statusText}\n\n${prettyBody}`);
  } catch (error) {
    setStatus(`Falha ao importar a sessão DeepSeek.\n\n${error.message || String(error)}`);
  }
}

async function importKimiSession() {
  const proxyUrl = normalizeProxyUrl(proxyUrlInput.value);
  if (!proxyUrl) { setStatus("Informe a URL do proxy antes de importar."); return; }
  setStatus("Lendo cookies e access_token do Kimi...");
  try {
    const [rawCookie, accessToken] = await Promise.all([collectKimiRawCookieJar(), getKimiAccessToken()]);
    const response = await fetch(`${proxyUrl}/auth/web/import`, {
      method: "POST", headers: providerHeaders(),
      body: JSON.stringify({ provider: "kimi", token: accessToken, rawCookie, user_agent: navigator.userAgent, storage: { access_token: accessToken }, source: "chrome-extension" })
    });
    const bodyText = await response.text();
    setStatus(`HTTP ${response.status} ${response.statusText}\n\n${bodyText}`);
  } catch (error) {
    setStatus(`Falha ao importar a sessão Kimi.\n\n${error.message || String(error)}`);
  }
}

function providerHeaders() {
  const headers = {
    "Content-Type": "application/json"
  };
  const apiKey = apiKeyInput.value.trim();
  if (apiKey) {
    headers.Authorization = `Bearer ${apiKey}`;
  }
  return headers;
}

async function saveProviderCredentials(provider, payload) {
  const proxyUrl = normalizeProxyUrl(proxyUrlInput.value);
  if (!proxyUrl) {
    setStatus("Informe a URL do proxy antes de salvar.");
    return;
  }

  await saveConfig();
  setStatus(`Salvando credenciais de ${provider}...`);

  try {
    const response = await fetch(`${proxyUrl}/auth/provider/import`, {
      method: "POST",
      headers: providerHeaders(),
      body: JSON.stringify({
        provider,
        ...payload
      })
    });

    const bodyText = await response.text();
    let prettyBody = bodyText;
    try {
      prettyBody = JSON.stringify(JSON.parse(bodyText), null, 2);
    } catch (error) {
      // Keep original text when response is not JSON.
    }

    setStatus(`HTTP ${response.status} ${response.statusText}\n\n${prettyBody}`);
  } catch (error) {
    setStatus(`Falha ao salvar ${provider}.\n\n${error.message || String(error)}`);
  }
}

saveConfigButton.addEventListener("click", saveConfig);
importButton.addEventListener("click", async () => {
  await saveConfig();
  await importSession();
});
importDeepSeekButton.addEventListener("click", async () => {
  await saveConfig();
  await importDeepSeekSession();
});
importKimiButton.addEventListener("click", async () => {
  await saveConfig();
  await importKimiSession();
});
openXiaomiButton.addEventListener("click", () => {
  chrome.tabs.create({ url: XIAOMI_STUDIO_URL });
});
openDeepSeekButton.addEventListener("click", () => {
  chrome.tabs.create({ url: DEEPSEEK_CHAT_URL });
});
openKimiButton.addEventListener("click", () => {
  chrome.tabs.create({ url: KIMI_CHAT_URL });
});
openGeminiButton.addEventListener("click", () => {
  chrome.tabs.create({ url: GEMINI_KEYS_URL });
});
openGroqButton.addEventListener("click", () => {
  chrome.tabs.create({ url: GROQ_KEYS_URL });
});
openOpenRouterButton.addEventListener("click", () => {
  chrome.tabs.create({ url: OPENROUTER_KEYS_URL });
});
openCloudflareButton.addEventListener("click", () => {
  chrome.tabs.create({ url: CLOUDFLARE_KEYS_URL });
});
saveGeminiButton.addEventListener("click", () => {
  saveProviderCredentials("gemini", {
    api_key: geminiApiKeyInput.value.trim()
  });
});
saveGroqButton.addEventListener("click", () => {
  saveProviderCredentials("groq", {
    api_key: groqApiKeyInput.value.trim()
  });
});
saveOpenRouterButton.addEventListener("click", async () => {
  await chrome.storage.local.set({
    openRouterReferer: openRouterRefererInput.value.trim(),
    openRouterTitle: openRouterTitleInput.value.trim()
  });
  saveProviderCredentials("openrouter", {
    api_key: openRouterApiKeyInput.value.trim(),
    http_referer: openRouterRefererInput.value.trim(),
    app_title: openRouterTitleInput.value.trim()
  });
});
saveCloudflareButton.addEventListener("click", () => {
  saveProviderCredentials("cloudflare", {
    api_key: cloudflareApiKeyInput.value.trim(),
    account_id: cloudflareAccountIdInput.value.trim()
  });
});

loadConfig();
