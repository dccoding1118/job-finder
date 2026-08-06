// The endpoint the Side Panel talks to. The accepted hosts are exactly the ones
// manifest.json grants host_permissions for: anything else would be stored
// happily and then fail at fetch time, which reads as "saved but offline" rather
// than as a rejected setting. `localhost` is not among them — it is a separate
// host string to Chrome's permission matcher, not an alias for 127.0.0.1 — and
// the IPv6 loopback arrives from URL parsing with its brackets attached.
const LOOPBACK_HOSTS = ["127.0.0.1", "[::1]"];

const endpoint = document.querySelector("#endpoint"), token = document.querySelector("#token"), status = document.querySelector("#status");
chrome.storage.local.get({ endpoint: "http://127.0.0.1:8686", token: "" }, (v) => { endpoint.value = v.endpoint; token.value = v.token; });
document.querySelector("#save").addEventListener("click", () => { try { const url = new URL(endpoint.value); if (url.protocol !== "http:" || !LOOPBACK_HOSTS.includes(url.hostname) || !token.value.trim()) throw new Error(); chrome.storage.local.set({ endpoint: url.origin, token: token.value.trim() }, () => { status.textContent = "已儲存"; token.value = ""; }); } catch { status.textContent = "請輸入 http://127.0.0.1:<port> 或 http://[::1]:<port> 與 token。"; } });
