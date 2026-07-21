// Captures the 104 detail page the user opened and shows the assessment in a
// sidebar. Screening comes back with the capture response; scoring is
// asynchronous, so the sidebar polls the local API until a verdict lands.
(() => {
  const POLL_INTERVAL_MS = 3000;
  const POLL_LIMIT_MS = 5 * 60 * 1000;
  // 104 renders the JobPosting JSON-LD from a Vue component, so it is often
  // absent at document_idle; the capture waits for it to land before reading.
  const CONTENT_WAIT_MS = 15000;
  const VERDICTS = { unfit: "不適合", recommended: "推薦", not_recommended: "不推薦", pending_score: "評分中", pending_detail: "待補全文" };

  const sidebar = document.createElement("div");
  sidebar.id = "jobfinder-sidebar";
  const shadow = sidebar.attachShadow({ mode: "open" });

  function render(body) {
    shadow.innerHTML = `<style>
      .panel { position: fixed; top: 12px; right: 12px; z-index: 2147483647; width: 280px; padding: 12px;
               border: 1px solid #d5d9e0; border-radius: 8px; background: #fff; color: #1f2328;
               font: 13px/1.5 system-ui, sans-serif; box-shadow: 0 4px 16px rgba(0,0,0,.12); }
      h2 { margin: 0 0 .4em; font-size: 14px; }
      dl { display: grid; grid-template-columns: auto 1fr; gap: .1em .6em; margin: .4em 0; }
      dt { color: #5f6368; }
      a { color: #1a5fb4; }
      p { margin: .4em 0; }
    </style><section class="panel" role="complementary" aria-label="jobfinder 評估">${body}</section>`;
  }

  function dimensions(score) {
    return `<dl>
      <dt>總分</dt><dd>${score.total}</dd>
      <dt>技能</dt><dd>${score.hard_skill}</dd>
      <dt>領域</dt><dd>${score.domain}</dd>
      <dt>資歷</dt><dd>${score.seniority}</dd>
      <dt>條件</dt><dd>${score.condition}</dd>
      <dt>方向</dt><dd>${score.direction}</dd>
    </dl><p>${escape(score.reason || "")}</p>`;
  }

  function escape(value) {
    const node = document.createElement("span");
    node.textContent = value;
    return node.innerHTML;
  }

  // The sidebar shows the assessment only; requesting a letter is a decision
  // the user makes in the extension page, against the full comparison.
  function show(state) {
    const verdict = VERDICTS[state.verdict] || "—";
    const hits = state.filter_hits?.length ? `<p>命中條件：${escape(state.filter_hits.join("、"))}</p>` : "";
    const cached = state.cached ? `<p>（快取結果）</p>` : "";
    const score = state.score ? dimensions(state.score) : "";
    let note = "";
    if (state.verdict === "pending_score") note = state.budget_exhausted ? "<p>已達今日評分上限，將於隔日評分。</p>" : "<p>評分中，結果會自動更新。</p>";
    if (state.timed_out) note = `<p>仍在處理，請至 extension page 查看。</p>`;
    const link = state.verdict === "recommended" ? `<p>推薦職缺的求職信在 extension page 產生。</p>` : "";
    render(`<h2>jobfinder：${verdict}</h2>${hits}${score}${cached}${note}${link}`);
  }

  function jsonLD() {
    return [...document.querySelectorAll('script[type="application/ld+json"]')].map((node) => node.textContent || "");
  }

  // The page is ready once a JobPosting block exists or the DOM fallback has
  // material; only then does reading it give the real content rather than the
  // empty shell 104 serves before the Vue app mounts.
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

  async function api(path, method = "GET", body) {
    return chrome.runtime.sendMessage({ type: "api", path, method, body });
  }

  async function poll(id, until) {
    if (Date.now() >= until) return show({ verdict: "pending_score", timed_out: true });
    const result = await api(`/api/v1/jobs/${id}`);
    if (!result?.ok) return render(`<h2>jobfinder</h2><p>${escape(result?.error || "無法連線到 jobfinder API。")}</p>`);
    const job = result.data;
    if (job.verdict === "pending_score") {
      setTimeout(() => poll(id, until), POLL_INTERVAL_MS);
      return show({ verdict: "pending_score" });
    }
    show({ verdict: job.verdict, score: job.score, filter_hits: job.filter_hits });
  }

  async function capture() {
    document.body.append(sidebar);
    render("<h2>jobfinder</h2><p>擷取中…</p>");
    await waitForContent();
    const blocks = jsonLD();
    const body = { url: location.href, json_ld: blocks };
    if (blocks.length === 0) {
      const dom = domFallback();
      if (!dom) return render("<h2>jobfinder</h2><p>無法從這個頁面取得職缺內容。</p>");
      body.dom = dom;
    }
    const result = await api("/api/v1/capture/job", "POST", body);
    if (!result?.ok) return render(`<h2>jobfinder</h2><p>${escape(result?.error || "擷取失敗，請重新整理頁面再試。")}</p>`);
    const state = result.data;
    show(state);
    // Screening needs no LLM, so `unfit` is already final here; only a job that
    // passed screening is waiting on the worker.
    if (state.verdict === "pending_score" && !state.budget_exhausted) {
      setTimeout(() => poll(state.id, Date.now() + POLL_LIMIT_MS), POLL_INTERVAL_MS);
    }
  }

  capture();
})();
