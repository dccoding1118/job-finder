const defaults = { endpoint: "http://127.0.0.1:8686", token: "" };

function enableActionSidePanel() {
  chrome.sidePanel?.setPanelBehavior({ openPanelOnActionClick: true }).catch(() => {});
}

chrome.runtime.onInstalled?.addListener(enableActionSidePanel);
enableActionSidePanel();

chrome.runtime.onMessage.addListener((message, _sender, respond) => {
  if (message?.type === "current-page") {
    chrome.tabs.query({ active: true, currentWindow: true }, async ([tab]) => {
      if (!tab?.id) return respond({ ok: true, context: { kind: "unsupported", status: "unsupported" } });
      try {
        const context = await chrome.tabs.sendMessage(tab.id, { type: "get-page-context" });
        respond({ ok: true, context });
      } catch (_error) {
        respond({ ok: true, context: { kind: "unsupported", status: "unsupported" } });
      }
    });
    return true;
  }
  if (message?.type !== "api") return;
  chrome.storage.local.get(defaults, async (settings) => {
    try {
      const response = await fetch(`${settings.endpoint}${message.path}`, {
        method: message.method || "GET",
        headers: { Authorization: `Bearer ${settings.token}`, "Content-Type": "application/json" },
        body: message.body ? JSON.stringify(message.body) : undefined,
      });
      const data = await response.json().catch(() => ({}));
      respond(response.ok ? { ok: true, data } : { ok: false, error: data.error?.message || "API request failed" });
    } catch (_error) { respond({ ok: false, error: "無法連線到 jobfinder API。請確認服務與 Options 設定。" }); }
  });
  return true;
});
