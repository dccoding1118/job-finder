// Harvests the Cake list the user has open and marks each item with the verdict
// the API returns. Cake's search itself is behind a challenge, so this is the
// only compliant way to reach it: the user picks the conditions in their own
// browser and this mode reads what that page shows.
globalThis.jobfinder = globalThis.jobfinder || {};
globalThis.jobfinder.cake = globalThis.jobfinder.cake || {};

globalThis.jobfinder.cake.list = (() => {
  const HARVEST_DEBOUNCE_MS = 400;
  const JOB_LINK = 'a[href^="/companies/"][href*="/jobs/"]';
  // Cake's class names carry a build hash, so only the stable prefix is matched.
  const ITEM_CONTAINER = '[class*="JobSearchItem"]';

  function nextData() {
    return document.querySelector("script#__NEXT_DATA__")?.textContent || "";
  }

  // The embedded state only ever describes the conditions the page was rendered
  // with: after the user changes a filter or turns a page it is stale, and the
  // DOM is then the only truthful source. Cake carries those conditions as an
  // object, and only its `query` and `page` can be compared against the URL, so
  // any other parameter — every patrol URL has one — reads as stale.
  function snapshotFresh(raw) {
    if (!raw) return false;
    try {
      const search = JSON.parse(raw)?.props?.pageProps?.ssr?.search;
      if (!search || typeof search !== "object") return false;
      if (Object.keys(search.filters || {}).length > 0) return false;
      const params = new URLSearchParams(location.search);
      const query = params.get("query") || "";
      const page = params.get("page") || "1";
      params.delete("query");
      params.delete("page");
      return [...params.keys()].length === 0 && query === (search.query || "") && page === String(search.page || 1);
    } catch {
      return false;
    }
  }

  function externalID(href) {
    const segments = new URL(href, location.href).pathname.split("/").filter(Boolean);
    if (segments[0] !== "companies" || segments[2] !== "jobs" || !segments[3]) return "";
    return `${segments[1]}/${segments[3]}`;
  }

  // itemContainer walks to the outermost `JobSearchItem…` ancestor. The nearest
  // one is not the item: Cake hashes that prefix onto every node of the entry,
  // the link's own heading included, so stopping at the first match would leave
  // the company name and the tags out of reach and the whole entry unreadable.
  function itemContainer(link) {
    let container = null;
    for (let node = link.parentElement; node; node = node.parentElement) {
      if (node.matches(ITEM_CONTAINER)) container = node;
    }
    return container || link.parentElement;
  }

  // items reads the visible entries off the DOM. Anything but the link path and
  // the text is read defensively: Cake renames its classes on every build.
  function items() {
    const found = new Map();
    for (const link of document.querySelectorAll(JOB_LINK)) {
      const id = externalID(link.href);
      if (!id || found.has(id)) continue;
      const container = itemContainer(link);
      if (!container) continue;
      // Each field is read from a leaf element rather than from innerText: Cake
      // renders the tags inline, so the text of a whole item collapses onto one
      // line and the location and the salary become indistinguishable.
      const fields = [...container.querySelectorAll("*")]
        .filter((element) => element.children.length === 0)
        .map((element) => (element.textContent || "").trim())
        .filter(Boolean);
      found.set(id, {
        node: container,
        item: {
          href: link.href,
          title: (link.innerText || link.textContent || "").trim(),
          company_name: companyName(container, fields),
          location: fields.find((field) => /市|縣|Taiwan|Remote/i.test(field)) || "",
          salary_text: fields.find((field) => /月薪|年薪|TWD|NT\$/.test(field)) || "",
        },
      });
    }
    return found;
  }

  // The item carries several links to the same company; the logo ones hold an
  // image and no text, so the first one that reads as a name is taken.
  function companyName(container, fields) {
    for (const link of container.querySelectorAll('a[href^="/companies/"]:not([href*="/jobs/"])')) {
      const name = (link.textContent || "").trim();
      if (name) return name;
    }
    return fields[1] || "";
  }

  return {
    start(publish) {
      const mark = globalThis.jobfinder.mark;
      const decisions = new Map();
      const requested = new Set();
      let pending = new Map();
      let timer = null;
      let stopped = false;

      publish({ kind: "list", source: "cake", status: "captured", title: "Cake 職缺清單" });

      function harvest() {
        if (stopped) return;
        for (const [id, entry] of items()) {
          const decision = decisions.get(id);
          if (decision) {
            mark(entry.node, decision);
            continue;
          }
          if (!requested.has(id)) pending.set(id, entry.item);
        }
        schedule();
      }

      function schedule() {
        if (stopped || timer || pending.size === 0) return;
        timer = setTimeout(send, HARVEST_DEBOUNCE_MS);
      }

      async function send() {
        timer = null;
        const batch = pending;
        pending = new Map();
        if (stopped || batch.size === 0) return;
        for (const id of batch.keys()) requested.add(id);
        const raw = nextData();
        const body = { source: "cake", url: location.href };
        // The embedded state is preferred whenever it is still current: its fields
        // are structured, where the DOM only carries rendered text.
        if (snapshotFresh(raw)) {
          body.next_data = raw;
        } else {
          body.items = [...batch.values()];
        }
        const response = await chrome.runtime.sendMessage({ type: "api", path: "/api/v1/capture/list", method: "POST", body });
        if (stopped) return;
        if (!response?.ok) {
          // A failed capture must not retry in the background: the items are
          // released so the user's next scroll or reload can ask again.
          for (const id of batch.keys()) requested.delete(id);
          console.warn("jobfinder: Cake 列表擷取失敗，捲動或重新整理可再試一次。", response?.error);
          return;
        }
        for (const item of response.data.items) decisions.set(item.external_id, item);
        harvest();
      }

      // Cake injects the results of a changed filter or a new page into the same
      // document, so harvesting continues for as long as the user stays.
      const observer = new MutationObserver(() => harvest());
      observer.observe(document.body, { childList: true, subtree: true });
      harvest();

      return {
        stop() {
          stopped = true;
          observer.disconnect();
          clearTimeout(timer);
        },
      };
    },
  };
})();
