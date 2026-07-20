// Harvests the 104 list the user has open and marks each item with the verdict
// the API returns. Everything here is triggered by the user's own navigation:
// no page is opened, no request reaches 104.
(() => {
  const VERDICTS = {
    unfit: { label: "不適合", background: "#fde8e8", color: "#8a1c1c", icon: "✕" },
    recommended: { label: "推薦", background: "#e6f6ea", color: "#0f5132", icon: "★" },
    not_recommended: { label: "不推薦", background: "#eceff1", color: "#5f6368", icon: "·" },
    pending_detail: { label: "待看", background: "#eef2ff", color: "#31407a", icon: "→" },
    pending_score: { label: "評分中", background: "#eef2ff", color: "#31407a", icon: "…" },
  };
  const HARVEST_DEBOUNCE_MS = 400;

  // The search page recycles the nodes it scrolls past, so an item can come
  // back with its mark gone; results stay cached by external id and are
  // reapplied whenever the node reappears.
  const decisions = new Map();
  const requested = new Set();
  let pending = new Map();
  let timer = null;

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

  function tagByPrefix(item, prefix) {
    for (const tag of item.querySelectorAll(".info-tags__text")) {
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

  function mark(node, decision) {
    const verdict = VERDICTS[decision.verdict];
    if (!verdict) return;
    let host = node.querySelector(":scope > .jobfinder-mark");
    if (!host) {
      host = document.createElement("div");
      host.className = "jobfinder-mark";
      host.attachShadow({ mode: "open" });
      node.prepend(host);
    }
    const detail = decision.filter_hits?.length
      ? `命中 ${decision.filter_hits.join("、")}`
      : decision.score_total != null
        ? `總分 ${decision.score_total}`
        : decision.verdict === "pending_detail"
          ? "點開內頁可取得完整評估"
          : "";
    // The mark never relies on color alone: the icon and the wording carry it.
    host.shadowRoot.innerHTML = `<style>
      .badge { display: inline-flex; gap: .4em; align-items: center; margin: .2em 0; padding: .15em .5em;
               border-radius: .4em; font: 600 12px/1.4 system-ui, sans-serif;
               background: ${verdict.background}; color: ${verdict.color}; }
    </style><p class="badge"><span aria-hidden="true">${verdict.icon}</span><span>jobfinder：${verdict.label}${detail ? `｜${detail}` : ""}</span></p>`;
    node.style.opacity = decision.verdict === "unfit" || decision.verdict === "not_recommended" ? "0.55" : "";
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
      body: { items: [...batch.values()] },
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
