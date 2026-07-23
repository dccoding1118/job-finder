const defaults = { endpoint: "http://127.0.0.1:8686", token: "" };

function enableActionSidePanel() {
  chrome.sidePanel?.setPanelBehavior({ openPanelOnActionClick: true }).catch(() => {});
}

chrome.runtime.onInstalled?.addListener(enableActionSidePanel);
enableActionSidePanel();

chrome.runtime.onMessage.addListener((message, sender, respond) => {
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
  if (message?.type !== "api" && message?.type !== "profile-api") return;
  const requestPath = new URL(message.path, "http://extension.invalid").pathname;
  const profilePath = requestPath === "/api/v1/profile" || requestPath.startsWith("/api/v1/profile/");
  const extensionPage = message.type !== "profile-api" || sender?.url?.startsWith(chrome.runtime.getURL(""));
  const allowedProfileRequest = (requestPath === "/api/v1/profile" && ["GET", "PUT"].includes(message.method))
    || (requestPath === "/api/v1/profile/reprocess" && message.method === "POST");
  if ((message.type === "profile-api" && (!allowedProfileRequest || !extensionPage)) || (message.type === "api" && profilePath)) {
    respond({ ok: false, status: 403, code: "profile_access_denied", error: "Profile 只能由 extension 使用者介面存取。" });
    return;
  }
  chrome.storage.local.get(defaults, async (settings) => {
    try {
      const headers = { Authorization: `Bearer ${settings.token}`, "Content-Type": "application/json" };
      if (message.type === "profile-api" && message.method === "PUT" && message.etag) headers["If-Match"] = message.etag;
      const response = await fetch(`${settings.endpoint}${message.path}`, {
        method: message.method || "GET",
        headers,
        body: message.body ? JSON.stringify(message.body) : undefined,
      });
      const data = await response.json().catch(() => ({}));
      if (message.type === "profile-api") {
        const error = data.error || {};
        respond(response.ok
          ? { ok: true, data, etag: response.headers.get("ETag") }
          : {
              ok: false,
              status: response.status,
              code: error.code || data.code || "profile_request_failed",
              error: error.message || data.message || "Profile request failed",
              issues: error.issues || error.details?.issues || (Array.isArray(error.details) ? error.details : null) || data.issues || [],
            });
        return;
      }
      respond(response.ok ? { ok: true, data } : { ok: false, status: response.status, code: data.error?.code, error: data.error?.message || "API request failed" });
    } catch (_error) { respond({ ok: false, error: "無法連線到 jobfinder API。請確認服務與 Options 設定。" }); }
  });
  return true;
});
