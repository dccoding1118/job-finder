// Captures the Cake detail page the user opened. The native Side Panel owns the
// assessment UI; this mode only reads the rendered JD and reports the current
// tab's capture status. Cake's detail pages carry no listing state to read — and
// the user arrives by client-side navigation, which leaves whatever state is on
// the page describing the list they came from — so the DOM is the only source.
globalThis.jobfinder = globalThis.jobfinder || {};
globalThis.jobfinder.cake = globalThis.jobfinder.cake || {};

globalThis.jobfinder.cake.job = (() => {
  const CONTENT_WAIT_MS = 15000;
  // Cake hashes a build id into every class name, so only the component prefix
  // and the role suffix are matched. The tag disambiguates where a prefix is
  // shared: `…__titleRow` would match a selector meant for `…__title`.
  const TITLE = 'h1[class*="JobDescriptionLeftColumn"]';
  const COMPANY = 'a[class*="JobDescriptionLeftColumn"][class*="__name"]';
  const SECTION = '[class*="ContentSection"][class*="contentSection"]';
  const SECTION_TITLE = 'h3[class*="ContentSection"]';
  const META = '[class*="rightColumn"], [class*="inlineJobMeta"]';

  function pageTitle() {
    return document.querySelector(TITLE)?.textContent?.trim() || document.querySelector("h1")?.textContent?.trim() || "";
  }

  function companyName() {
    const named = document.querySelector(COMPANY)?.textContent?.trim();
    if (named) return named;
    // The listing carries several links to the company; the logo one holds an
    // image and no text, so the first one that reads as a name is taken.
    for (const link of document.querySelectorAll('a[href^="/companies/"]:not([href*="/jobs/"])')) {
      const name = (link.textContent || "").trim();
      if (name) return name;
    }
    return "";
  }

  // sections reads the JD blocks in page order. The heading is kept with its
  // body so the assessment reads requirements as requirements.
  function sections() {
    const found = [];
    for (const node of document.querySelectorAll(SECTION)) {
      // `:scope >` keeps a section from matching itself: its own class name
      // carries the content suffix the body is matched by.
      const body = node.querySelector(':scope > [class*="__content"]');
      const text = (body?.innerText || "").trim();
      if (!text) continue;
      found.push({ title: node.querySelector(SECTION_TITLE)?.textContent?.trim() || "", body: text });
    }
    return found;
  }

  // meta harvests the short lines of the listing's metadata areas. Cake renders
  // the location, the salary, and the work arrangement as free text whose
  // position is not stable, so the lines are sent verbatim and recognized by the
  // API rather than assigned to fields here.
  function meta() {
    const lines = [];
    for (const area of document.querySelectorAll(META)) {
      for (const element of area.querySelectorAll("*")) {
        if (element.children.length > 0) continue;
        const text = (element.textContent || "").trim();
        if (text && text.length <= 80 && !lines.includes(text)) lines.push(text);
      }
    }
    return lines;
  }

  function harvest() {
    return { title: pageTitle(), company_name: companyName(), sections: sections(), meta: meta() };
  }

  function ready(dom) {
    return Boolean(dom.title && dom.company_name && dom.sections.length > 0);
  }

  return {
    start(publish) {
      let stopped = false;
      let observer = null;
      let timer = null;

      const done = () => {
        stopped = true;
        observer?.disconnect();
        clearTimeout(timer);
      };

      const waitForContent = () => new Promise((resolve) => {
        if (ready(harvest())) return resolve();
        observer = new MutationObserver(() => {
          if (!ready(harvest())) return;
          observer.disconnect();
          clearTimeout(timer);
          resolve();
        });
        observer.observe(document.documentElement, { childList: true, subtree: true });
        timer = setTimeout(() => {
          observer.disconnect();
          resolve();
        }, CONTENT_WAIT_MS);
      });

      const capture = async () => {
        publish({ kind: "job", source: "cake", status: "capturing", title: document.title });
        await waitForContent();
        if (stopped) return;
        const dom = harvest();
        if (!ready(dom)) {
          publish({ kind: "job", source: "cake", status: "error", title: document.title, error: "無法從這個頁面取得職缺內容。" });
          return;
        }
        const result = await chrome.runtime.sendMessage({
          type: "api",
          path: "/api/v1/capture/job",
          method: "POST",
          body: { source: "cake", url: location.href, cake_dom: dom },
        });
        if (stopped) return;
        if (!result?.ok) {
          publish({ kind: "job", source: "cake", status: "error", title: document.title, error: result?.error || "擷取失敗，請重新整理頁面再試。" });
          return;
        }
        publish({
          kind: "job",
          source: "cake",
          status: "captured",
          title: dom.title || document.title,
          job_id: result.data.id,
          verdict: result.data.verdict,
          budget_exhausted: Boolean(result.data.budget_exhausted),
        });
      };

      capture();
      return { stop: done };
    },
  };
})();
