// Harvests the Cake list the user has open and marks each item with the verdict
// the API returns. Cake's search itself is behind a challenge, so this is the
// only compliant way to reach it: the user picks the conditions in their own
// browser and this mode reads what that page shows.
globalThis.jobfinder = globalThis.jobfinder || {};
globalThis.jobfinder.cake = globalThis.jobfinder.cake || {};

globalThis.jobfinder.cake.list = (() => {
  // A capture is sent once the list stops changing, so a page turn is read from
  // a settled DOM rather than mid-render; the cap keeps a continuously changing
  // page (infinite scroll) from starving the send.
  const SETTLE_MS = 400;
  const MAX_WAIT_MS = 1500;
  // An item the API answered nothing for is asked again a bounded number of
  // times. Without this a single incomplete read would leave that entry unmarked
  // until the user reloaded the document.
  const MAX_ATTEMPTS = 3;
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
    // A link with no such ancestor is not a search result — Cake links the same
    // job from its recommendation and recently-viewed blocks too. Its nearest
    // parent is still marked so the user sees the verdict wherever the job is
    // shown, but a result entry is what the fields are read from.
    return { container: container || link.parentElement, isResult: container !== null };
  }

  // read pulls one entry's fields off its container. Each field comes from a leaf
  // element rather than from innerText: Cake renders the tags inline, so the text
  // of a whole item collapses onto one line and the location and the salary
  // become indistinguishable.
  function read(container, link) {
    const fields = [...container.querySelectorAll("*")]
      .filter((element) => element.children.length === 0)
      .map((element) => (element.textContent || "").trim())
      .filter(Boolean);
    return {
      href: link.href,
      title: (link.innerText || link.textContent || "").trim(),
      company_name: companyName(container, fields),
      location: fields.find((field) => /市|縣|Taiwan|Remote/i.test(field)) || "",
      salary_text: fields.find((field) => /月薪|年薪|TWD|NT\$/.test(field)) || "",
    };
  }

  // items reads the visible entries off the DOM. Anything but the link path and
  // the text is read defensively: Cake renames its classes on every build.
  //
  // One job can be linked from several places on the same page, so every node an
  // id appears in is collected: marking only the first occurrence would leave the
  // result entry of an already-known job bare while its mark went to a
  // recommendation block instead.
  function items() {
    const found = new Map();
    for (const link of document.querySelectorAll(JOB_LINK)) {
      const id = externalID(link.href);
      if (!id) continue;
      const { container, isResult } = itemContainer(link);
      if (!container) continue;
      const entry = found.get(id) || { nodes: [], item: null, fromResult: false };
      if (!entry.nodes.includes(container)) entry.nodes.push(container);
      // The fields of a result entry outrank those of any other occurrence: only
      // a result entry carries the company, location and salary.
      if (!entry.item || (isResult && !entry.fromResult)) {
        entry.item = read(container, link);
        entry.fromResult = isResult;
      }
      found.set(id, entry);
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
      // inflight holds the ids of the batch being asked about right now;
      // attempts counts how often each id has been asked, so a retry cannot loop.
      const inflight = new Set();
      const attempts = new Map();
      let pending = new Map();
      let timer = null;
      let deadline = 0;
      let stopped = false;
      let currentURL = location.href;

      publish({ kind: "list", source: "cake", status: "captured", title: "Cake 職缺清單" });

      function harvest() {
        if (stopped) return;
        // Turning a page or changing a filter renders a different result set into
        // the same document, so every entry gets its full retry budget again.
        if (location.href !== currentURL) {
          currentURL = location.href;
          attempts.clear();
        }
        for (const [id, entry] of items()) {
          const decision = decisions.get(id);
          if (decision) {
            for (const node of entry.nodes) mark(node, decision);
            continue;
          }
          if (!inflight.has(id) && (attempts.get(id) || 0) < MAX_ATTEMPTS) pending.set(id, entry.item);
        }
        schedule();
      }

      function schedule() {
        if (stopped || pending.size === 0) return;
        const now = Date.now();
        if (timer) clearTimeout(timer);
        else deadline = now + MAX_WAIT_MS;
        timer = setTimeout(send, Math.max(0, Math.min(SETTLE_MS, deadline - now)));
      }

      async function send() {
        timer = null;
        const batch = pending;
        pending = new Map();
        if (stopped || batch.size === 0) return;
        for (const id of batch.keys()) {
          inflight.add(id);
          attempts.set(id, (attempts.get(id) || 0) + 1);
        }
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
        for (const id of batch.keys()) inflight.delete(id);
        if (!response?.ok) {
          // A failed capture must not retry in the background, and a failure is
          // not the item's fault: the attempt is given back so the user's next
          // scroll or navigation can ask again.
          for (const id of batch.keys()) attempts.set(id, Math.max(0, (attempts.get(id) || 1) - 1));
          console.warn("jobfinder: Cake 列表擷取失敗，捲動或重新整理可再試一次。", response?.error);
          return;
        }
        for (const item of response.data.items) decisions.set(item.external_id, item);
        // Ids the response said nothing about stay unmarked; they are left
        // pending-eligible so the next harvest asks again within the budget.
        harvest();
      }

      // Cake injects the results of a changed filter or a new page into the same
      // document, so harvesting continues for as long as the user stays.
      const observer = new MutationObserver(() => harvest());
      observer.observe(document.body, { childList: true, subtree: true });
      // Opening a job changes its verdict, and that happens in another tab: the
      // cached decisions of this list are stale the moment the user comes back.
      // They are dropped on return so the marks are asked for again — a job that
      // is already known is only read, never re-screened, so this costs no Agent
      // call.
      const refresh = () => {
        if (stopped || document.visibilityState !== "visible") return;
        decisions.clear();
        attempts.clear();
        harvest();
      };
      document.addEventListener("visibilitychange", refresh);
      harvest();

      return {
        stop() {
          stopped = true;
          observer.disconnect();
          document.removeEventListener("visibilitychange", refresh);
          clearTimeout(timer);
        },
      };
    },
  };
})();
