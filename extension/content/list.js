// Harvests the 104 list the user has open and marks each item with the verdict
// the API returns. Everything here is triggered by the user's own navigation:
// no page is opened, no request reaches 104.
(() => {
  const HARVEST_DEBOUNCE_MS = 400;
  const mark = globalThis.jobfinder.mark;

  // The search page recycles the nodes it scrolls past, so an item can come
  // back with its mark gone; results stay cached by external id and are
  // reapplied whenever the node reappears.
  const decisions = new Map();
  const requested = new Set();
  let pending = new Map();
  let timer = null;
  const pageContext = { kind: "list", source: "104", status: "captured", title: "104 職缺清單" };

  chrome.runtime.onMessage.addListener((message, _sender, respond) => {
    if (message?.type !== "get-page-context") return;
    respond(pageContext);
  });

  const searchPage = {
    itemSelector: ".job-summary",
    read(item) {
      const link = item.querySelector("a.info-job__text");
      if (!link) return null;
      return {
        href: link.href,
        // The node text is chopped up by keyword highlight spans; the title
        // attribute holds the whole job title.
        title: link.getAttribute("title") || "",
        company_name: item.querySelector("a.info-company__text")?.textContent || "",
        company_info: item.querySelector(".info-company-addon-type")?.textContent || "",
        location: tagByPrefix(item, "職缺-地區-"),
        salary_text: tagByPrefix(item, "職缺-薪資-"),
        remote: (item.querySelector(".info-othertags")?.textContent || "").includes("遠端工作"),
      };
    },
  };

  const notificationPage = {
    itemSelector: ".job-list-container[pagenumber]",
    read(item) {
      const link = item.querySelector("a.info-job__text");
      if (!link) return null;
      // This page carries no data-gtm-joblist attributes: the tags are ordered
      // (area, experience, education) and the salary is mixed into the other
      // tags, so it is recognized by format rather than position.
      const tags = [...item.querySelectorAll(".info-tags__text")].map((tag) => tag.textContent.trim());
      const others = [...item.querySelectorAll(".info-othertags__text")].map((tag) => tag.textContent.trim());
      return {
        href: link.href,
        title: link.getAttribute("title") || "",
        company_name: item.querySelector("a.info-company__text")?.textContent || "",
        company_info: item.querySelector(".info-company-addon-type")?.textContent || "",
        location: tags[0] || "",
        salary_text: others.find((tag) => /月薪|年薪|時薪|待遇/.test(tag)) || "",
        remote: others.some((tag) => tag.includes("遠端工作")),
      };
    },
  };

  // The gtm marker sits on the anchor 104 wraps inside each .info-tags__text
  // span, not on the span itself, so the whole subtree is scanned by attribute.
  function tagByPrefix(item, prefix) {
    for (const tag of item.querySelectorAll("[data-gtm-joblist]")) {
      if ((tag.getAttribute("data-gtm-joblist") || "").startsWith(prefix)) return tag.textContent.trim();
    }
    return "";
  }

  function page() {
    if (location.hostname === "pda.104.com.tw") return notificationPage;
    return searchPage;
  }

  function externalID(href) {
    return new URL(href, location.href).pathname.match(/\/job\/([A-Za-z0-9]+)/)?.[1] || "";
  }

  // The first search result is an ad rather than a result; its link carries a
  // hotjob job source.
  function sponsored(href) {
    return (new URL(href, location.href).searchParams.get("jobsource") || "").startsWith("hotjob");
  }

  function harvest() {
    for (const node of document.querySelectorAll(page().itemSelector)) {
      const item = page().read(node);
      if (!item?.href || sponsored(item.href)) continue;
      const id = externalID(item.href);
      if (!id) continue;
      const decision = decisions.get(id);
      if (decision) {
        mark(node, decision);
        continue;
      }
      if (!requested.has(id)) pending.set(id, item);
    }
    schedule();
  }

  function schedule() {
    if (timer || pending.size === 0) return;
    timer = setTimeout(send, HARVEST_DEBOUNCE_MS);
  }

  async function send() {
    timer = null;
    const batch = pending;
    pending = new Map();
    if (batch.size === 0) return;
    for (const id of batch.keys()) requested.add(id);
    const response = await chrome.runtime.sendMessage({
      type: "api",
      path: "/api/v1/capture/list",
      method: "POST",
      body: { source: "104", url: location.href, items: [...batch.values()] },
    });
    if (!response?.ok) {
      // A failed capture must not retry in the background: the items are
      // released so the user's next scroll or reload can ask again.
      for (const id of batch.keys()) requested.delete(id);
      console.warn("jobfinder: 列表擷取失敗，捲動或重新整理可再試一次。", response?.error);
      return;
    }
    for (const item of response.data.items) decisions.set(item.external_id, item);
    harvest();
  }

  const observer = new MutationObserver(() => harvest());
  observer.observe(document.body, { childList: true, subtree: true });
  harvest();
})();
