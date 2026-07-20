const defaults = { endpoint: "http://127.0.0.1:8686", token: "" };

chrome.runtime.onMessage.addListener((message, _sender, respond) => {
  if (message?.type !== "api") return;
  chrome.storage.local.get(defaults, async (settings) => {
    try {
      const response = await fetch(`${settings.endpoint}${message.path}`, {
        method: message.method || "GET",
        headers: { Authorization: `Bearer ${settings.token}`, "Content-Type": "application/json" },
        body: message.body ? JSON.stringify(message.body) : undefined,
      });
      const data = await response.json();
      respond(response.ok ? { ok: true, data } : { ok: false, error: data.error?.message || "API request failed" });
    } catch (_error) { respond({ ok: false, error: "無法連線到 jobfinder API。請確認服務與 Options 設定。" }); }
  });
  return true;
});
