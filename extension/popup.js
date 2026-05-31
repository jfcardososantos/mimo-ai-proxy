const proxyUrlInput = document.getElementById("proxyUrl");
const apiKeyInput = document.getElementById("apiKey");
const statusOutput = document.getElementById("status");
const saveConfigButton = document.getElementById("saveConfig");
const importButton = document.getElementById("importSession");
const openXiaomiButton = document.getElementById("openXiaomi");

const XIAOMI_STUDIO_URL = "https://aistudio.xiaomimimo.com/";
const COOKIE_NAMES = ["serviceToken", "userId", "xiaomichatbot_ph"];
const COOKIE_URLS = [
  "https://aistudio.xiaomimimo.com/",
  "https://xiaomimimo.com/",
  "https://account.xiaomi.com/"
];

function setStatus(message) {
  statusOutput.value = message;
}

function normalizeProxyUrl(url) {
  return url.trim().replace(/\/+$/, "");
}

async function loadConfig() {
  const stored = await chrome.storage.local.get(["proxyUrl", "apiKey"]);
  proxyUrlInput.value = stored.proxyUrl || "";
  apiKeyInput.value = stored.apiKey || "";
  setStatus("Configure a URL do proxy e clique em Import Xiaomi Session.");
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

async function collectSession() {
  const found = {};

  for (const name of COOKIE_NAMES) {
    const cookie = await getCookieByName(name);
    if (!cookie || !cookie.value) {
      throw new Error(`Missing Xiaomi cookie: ${name}`);
    }
    found[name] = cookie.value;
  }

  const rawCookie = `serviceToken="${found.serviceToken}"; userId=${found.userId}; xiaomichatbot_ph="${found.xiaomichatbot_ph}"`;

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

saveConfigButton.addEventListener("click", saveConfig);
importButton.addEventListener("click", async () => {
  await saveConfig();
  await importSession();
});
openXiaomiButton.addEventListener("click", () => {
  chrome.tabs.create({ url: XIAOMI_STUDIO_URL });
});

loadConfig();
