// Captures the 104 detail page the user opened. The native Side Panel owns the
// full assessment UI; this script only exposes the current tab's capture state.
(() => {
  const CONTENT_WAIT_MS = 15000;
  let pageContext = { kind: "job", source: "104", status: "capturing", title: document.title };

  chrome.runtime.onMessage.addListener((message, _sender, respond) => {
    if (message?.type !== "get-page-context") return;
    respond(pageContext);
  });

  function publish(context) {
    pageContext = context;
    chrome.runtime.sendMessage({ type: "page-context-updated", context }).catch(() => {});
  }

  function jsonLD() {
    return [...document.querySelectorAll('script[type="application/ld+json"]')].map((node) => node.textContent || "");
  }

  function domFallback() {
    const description = document.querySelector(".job-description, .content")?.textContent || "";
    if (!description.trim()) return null;
    return {
      title: document.querySelector("h1")?.textContent || "",
      company_name: document.querySelector('a[href*="/company/"]')?.textContent || "",
      company_info: "",
      location: document.querySelector('[data-gtm-jobpage*="地區"]')?.textContent || "",
      description,
      salary_text: document.querySelector('[data-gtm-jobpage*="待遇"]')?.textContent || "",
      remote: document.body.textContent.includes("遠端工作"),
    };
  }

  function contentReady() {
    return jsonLD().some((block) => block.includes("JobPosting")) || domFallback() != null;
  }

  function waitForContent() {
    return new Promise((resolve) => {
      if (contentReady()) return resolve();
      const observer = new MutationObserver(() => {
        if (!contentReady()) return;
        observer.disconnect();
        clearTimeout(timer);
        resolve();
      });
      observer.observe(document.documentElement, { childList: true, subtree: true });
      const timer = setTimeout(() => {
        observer.disconnect();
        resolve();
      }, CONTENT_WAIT_MS);
    });
  }

  async function capture() {
    await waitForContent();
    const blocks = jsonLD();
    const body = { url: location.href, json_ld: blocks };
    if (blocks.length === 0) {
      const dom = domFallback();
      if (!dom) {
        publish({ kind: "job", source: "104", status: "error", title: document.title, error: "無法從這個頁面取得職缺內容。" });
        return;
      }
      body.dom = dom;
    }
    const result = await chrome.runtime.sendMessage({ type: "api", path: "/api/v1/capture/job", method: "POST", body });
    if (!result?.ok) {
      publish({ kind: "job", source: "104", status: "error", title: document.title, error: result?.error || "擷取失敗，請重新整理頁面再試。" });
      return;
    }
    publish({
      kind: "job",
      source: "104",
      status: "captured",
      title: document.querySelector("h1")?.textContent?.trim() || document.title,
      job_id: result.data.id,
      verdict: result.data.verdict,
      budget_exhausted: Boolean(result.data.budget_exhausted),
    });
  }

  capture();
})();
