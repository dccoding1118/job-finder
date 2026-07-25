(() => {
  const POLL_INTERVAL_MS = 3000;
  const POLL_LIMIT_MS = 5 * 60 * 1000;
  const VERDICTS = {
    unfit: { label: "不適合", tone: "negative", icon: "close" },
    recommended: { label: "推薦", tone: "positive", icon: "check" },
    not_recommended: { label: "不推薦", tone: "neutral", icon: "close" },
    pending_detail: { label: "待補全文", tone: "warning", icon: "clock" },
    pending_score: { label: "評分中", tone: "warning", icon: "clock" },
  };
  const icons = {
    check: '<path d="m5 12 4 4L19 6" />',
    close: '<path d="m7 7 10 10M17 7 7 17" />',
    clock: '<circle cx="12" cy="12" r="8" /><path d="M12 8v4l3 2" />',
    pin: '<path d="M20 10c0 5-8 11-8 11S4 15 4 10a8 8 0 1 1 16 0Z" /><circle cx="12" cy="10" r="2.5" />',
    wallet: '<rect x="3" y="6" width="18" height="13" rx="3" /><path d="M16 10h5v5h-5a2.5 2.5 0 0 1 0-5Z" />',
    arrow: '<path d="m9 18 6-6-6-6" />',
    external: '<path d="M14 5h5v5M19 5l-8 8" /><path d="M18 13v5a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5" />',
    spark: '<path d="m12 3 1.4 4.1L18 9l-4.6 1.9L12 15l-1.4-4.1L6 9l4.6-1.9L12 3Z" /><path d="m18.5 15 .7 2 1.8.8-1.8.7-.7 2-.7-2-1.8-.7 1.8-.8.7-2Z" />',
    copy: '<rect x="8" y="8" width="11" height="11" rx="2" /><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h2" />',
    info: '<circle cx="12" cy="12" r="9" /><path d="M12 11v5M12 8h.01" />',
    cloud: '<path d="M7 18h10a4 4 0 0 0 .8-7.9A6 6 0 0 0 6.4 8.3 4.8 4.8 0 0 0 7 18Z" />',
    play: '<path d="m9 7 8 5-8 5V7Z" />',
    refresh: '<path d="M20 7v5h-5M4 17v-5h5M6.1 8.2A7 7 0 0 1 18.4 7L20 9M4 15l1.6 2A7 7 0 0 0 18 15.8" />',
    moon: '<path d="M20 15.2A8.2 8.2 0 0 1 8.8 4 8.5 8.5 0 1 0 20 15.2Z" />',
    sun: '<circle cx="12" cy="12" r="3.5" /><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" />',
  };

  const state = {
    activeTab: "current",
    theme: "light",
    connected: null,
    context: { kind: "loading", status: "loading" },
    currentJob: null,
    jobs: [],
    jobsNextCursor: null,
    viewedJobIDs: new Set(),
    queue: [],
    runs: [],
    progress: null,
    profile: null,
    filters: { verdict: "recommended", process: "", apply: "", source: "" },
    filtersOpen: false,
    scrollPositions: { current: 0, queue: 0, shortlist: 0, system: 0 },
    collectionRequest: 0,
    busy: new Set(),
    toastTimer: null,
    pollTimer: null,
    pollUntil: 0,
  };

  const screens = [...document.querySelectorAll("[data-screen]")];
  const tabs = [...document.querySelectorAll("[data-tab]")];
  const contextNode = document.querySelector("#page-context");
  const connection = document.querySelector("#connection");
  const themeToggle = document.querySelector("#theme-toggle");
  const refresh = document.querySelector("#refresh");
  const status = document.querySelector("#status");
  const toast = document.querySelector("#toast");
  refresh.innerHTML = icon("refresh");

  function icon(name, className = "") {
    return `<svg class="${className}" viewBox="0 0 24 24" aria-hidden="true">${icons[name] || icons.info}</svg>`;
  }

  function escapeHTML(value) {
    return String(value ?? "")
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll('"', "&quot;")
      .replaceAll("'", "&#039;");
  }

  function text(value) {
    return value == null || value === "" ? "—" : String(value);
  }

  function displayScore(value) {
    const score = Number(value);
    return value == null || value === "" || !Number.isFinite(score) ? "—" : String(Math.round(score));
  }

  function shortRevision(value) {
    return value ? String(value).replace(/^sha256:/, "").slice(0, 8) : "未知";
  }

  function scoreRevisionBadge(job) {
    const total = job.score?.total ?? job.score_total;
    if (total == null) return "";
    const revision = job.score_profile_revision || job.evaluation_profile_revision;
    const stale = Boolean(job.score_stale);
    return `<span class="revision-badge ${stale ? "is-stale" : "is-current"}">Profile ${escapeHTML(shortRevision(revision))} · ${stale ? "待重評" : "最新"}</span>`;
  }

  function salary(job) {
    if (job.salary_min == null && job.salary_max == null) return "薪資未提供";
    if (job.salary_min != null && job.salary_max != null) return `${job.salary_min.toLocaleString()}–${job.salary_max.toLocaleString()}`;
    return job.salary_min != null ? `${job.salary_min.toLocaleString()} 以上` : `${job.salary_max.toLocaleString()} 以下`;
  }

  function verdictMeta(job) {
    return VERDICTS[job?.verdict] || { label: "等待判定", tone: "neutral", icon: "clock" };
  }

  function api(path, method = "GET", body) {
    return chrome.runtime.sendMessage({ type: "api", path, method, body });
  }

  function extensionMessage(message) {
    return chrome.runtime.sendMessage(message).catch(() => ({ ok: false }));
  }

  function storageGet(defaults) {
    return new Promise((resolve) => chrome.storage.local.get(defaults, resolve));
  }

  function storageSet(values) {
    return new Promise((resolve) => chrome.storage.local.set(values, resolve));
  }

  function setConnection(connected) {
    state.connected = connected;
    connection.classList.toggle("is-offline", connected === false);
    connection.lastElementChild.textContent = connected === null ? "連線中" : connected ? "已連線" : "離線";
  }

  function announce(message) {
    status.textContent = message;
  }

  function showToast(message) {
    clearTimeout(state.toastTimer);
    announce(message);
    toast.textContent = message;
    toast.hidden = false;
    state.toastTimer = setTimeout(() => { toast.hidden = true; }, 2600);
  }

  function applyTheme() {
    const dark = state.theme === "dark";
    document.documentElement.dataset.theme = state.theme;
    themeToggle.innerHTML = icon(dark ? "sun" : "moon");
    themeToggle.setAttribute("aria-label", dark ? "切換至淺色模式" : "切換至深色模式");
    themeToggle.setAttribute("aria-pressed", String(dark));
  }

  function updateHeader() {
    const job = state.currentJob;
    const page = state.context;
    let source = page.source || job?.source || "—";
    let title = job?.title || page.title || "目前分頁沒有可顯示的職缺";
    let subtitle = job?.company_name || (page.kind === "list" ? "104 職缺清單" : "切換至支援的職缺頁，或從推薦清單選取");
    let captureLabel = job ? (page.job_id === job.id ? "已擷取" : "已選取") : page.status === "capturing" ? "擷取中" : "無職缺";
    let captureIcon = job ? "check" : "clock";
    contextNode.innerHTML = `
      <span class="source-badge">${escapeHTML(source)}</span>
      <span class="context-copy"><strong>${escapeHTML(title)}</strong><span>${escapeHTML(subtitle)}</span></span>
      <span class="capture-badge">${icon(captureIcon)}<span>${escapeHTML(captureLabel)}</span></span>
    `;
  }

  function switchTab(name) {
    if (name !== state.activeTab) state.scrollPositions[state.activeTab] = window.scrollY;
    state.activeTab = name;
    tabs.forEach((tab) => {
      const active = tab.dataset.tab === name;
      tab.classList.toggle("is-active", active);
      tab.setAttribute("aria-selected", String(active));
      tab.tabIndex = active ? 0 : -1;
    });
    screens.forEach((screen) => { screen.hidden = screen.dataset.screen !== name; });
    window.scrollTo({ top: state.scrollPositions[name] || 0, behavior: "auto" });
  }

  function emptyState(title, copy) {
    return `<div class="empty-state"><span class="empty-icon">${icon("info")}</span><strong>${escapeHTML(title)}</strong><p>${escapeHTML(copy)}</p></div>`;
  }

  function scoreCard(job) {
    if (!job.score) return "";
    const dimensions = [
      ["技能", job.score.hard_skill], ["領域", job.score.domain], ["資歷", job.score.seniority],
      ["條件", job.score.condition], ["方向", job.score.direction],
    ];
    const rows = dimensions.map(([label, value]) => `
      <div class="score-row">
        <div class="score-row-label"><span>${label}</span><strong>${displayScore(value)}</strong></div>
        <div class="score-track" role="progressbar" aria-label="${label}評分" aria-valuenow="${Number(value) || 0}" aria-valuemin="0" aria-valuemax="100"><span style="--score: ${Number(value) || 0}%"></span></div>
      </div>`).join("");
    return `<section class="card" aria-labelledby="score-title"><div class="card-heading"><h2 id="score-title">五維評分</h2><span>0–100</span></div><div id="score" class="score-list" aria-label="總分 ${text(job.score.total)}">${rows}</div></section>`;
  }

  function trackingFields(job) {
    return `
      <div class="select-row"><label for="apply-state">投遞狀態</label><select id="apply-state">
        ${[["pending", "待投遞"], ["applied", "已投遞"], ["interview", "面試"], ["offer", "錄取"], ["ghosted", "無回音"], ["dropped", "放棄"]].map(([value, label]) => `<option value="${value}" ${job.apply_state === value ? "selected" : ""}>${label}</option>`).join("")}
      </select></div>
      <button id="save-apply" class="button is-secondary is-full" type="button" ${state.connected === false || state.busy.has("apply") ? "disabled" : ""}>更新投遞狀態</button>
      <details><summary class="helper-text">狀態記錄</summary><div id="events" class="event-list">${(job.status_events || []).map((event) => `<p>${escapeHTML(event.axis)}：${escapeHTML(event.from_state || "—")} → ${escapeHTML(event.to_state)}</p>`).join("") || "<p>尚無狀態記錄</p>"}</div></details>`;
  }

  function letterCard(job) {
    const letterReady = job.letter_state === "ready" && job.letter?.status === "approved";
    const requested = job.letter_state === "requested";
    const failed = job.letter_state === "failed";
    if (!job.letter_state) return `<section class="card"><div class="letter-heading"><h2>投遞追蹤</h2></div>${trackingFields(job)}</section>`;
    let body = "";
    let badge = "按需生成";
    if (letterReady) {
      badge = `${icon("check")}已過審`;
      body = `<pre id="letter" class="letter-content">${escapeHTML(job.letter.content)}</pre>`;
    } else if (requested) {
      badge = '<span class="spinner" aria-hidden="true"></span>產生中';
      body = '<p class="letter-copy">要求已送出。常駐 worker 完成起草與審查後，重新整理即可取得信件。</p><pre id="letter" class="visually-hidden"></pre>';
    } else {
      body = `<p class="letter-copy">${failed ? "上次信件未通過審查，可再次要求產生。" : "確認想投遞後才產生，避免消耗 Agent 額度。"}</p><pre id="letter" class="visually-hidden"></pre>`;
    }
    return `
      <section class="card letter-card" aria-labelledby="letter-title">
        <div class="letter-heading"><h2 id="letter-title">求職信</h2><span id="letter-state" class="letter-state ${letterReady ? "is-ready" : requested ? "is-pending" : ""}">${badge}</span></div>
        ${body}
        ${trackingFields(job)}
      </section>`;
  }

  function canRescore(job) {
    return ["scored", "shortlisted"].includes(job.process_state);
  }

  function rescoreButton(job) {
    if (!canRescore(job) || state.connected === false) return "";
    const busy = state.busy.has("rescore");
    return `<button id="rescore" class="button is-secondary" type="button" ${busy ? "disabled" : ""} aria-label="重新評分這筆職缺">${busy ? '<span class="spinner" aria-hidden="true"></span>' : icon("refresh")}重新評分</button>`;
  }

  function actionDock(job) {
    const source = `<a id="source" class="button is-secondary" href="${escapeHTML(job.url || "#")}" target="_blank" rel="noreferrer" aria-label="開啟原始職缺">${icon("external")}</a>`;
    if (state.connected === false) return `<div class="action-dock">${source}<button class="button is-primary" type="button" disabled>${icon("cloud")}等待重新連線</button></div>`;
    if (job.letter_state === "ready" && job.letter?.status === "approved") return `<div class="action-dock">${source}<button id="copy" class="button is-primary" type="button">${icon("copy")}複製求職信</button></div>`;
    if (job.letter_state === "requested") return `<div class="action-dock">${source}<button id="request-letter" class="button is-primary" type="button" disabled><span class="spinner" aria-hidden="true"></span>求職信產生中</button></div>`;
    if (job.verdict === "recommended") return `<div class="action-dock">${source}${rescoreButton(job)}<button id="request-letter" class="button is-primary" type="button" ${state.busy.has("letter") ? "disabled" : ""}>${icon("spark")}${job.letter_state === "failed" ? "再次產生求職信" : "產生求職信"}</button></div>`;
    if (job.verdict === "pending_score") return `<div class="action-dock">${source}<button class="button is-primary" type="button" disabled><span class="spinner" aria-hidden="true"></span>正在評分</button></div>`;
    return `<div class="action-dock">${source}${rescoreButton(job)}<button id="next-job" class="button is-primary" type="button">開下一筆${icon("arrow")}</button></div>`;
  }

  function renderCurrent() {
    const root = document.querySelector("#screen-current");
    const job = state.currentJob;
    if (!job) {
      const message = state.context.status === "capturing"
        ? emptyState("正在擷取目前職缺", "完成後會在這裡顯示判定與評分。")
        : state.context.status === "error"
          ? `<div class="notice is-offline" role="alert"><div class="notice-title">${icon("cloud")}擷取失敗</div><p>${escapeHTML(state.context.error || "請重新整理目前職缺頁再試一次。")}</p></div>`
          : emptyState("目前沒有職缺", "開啟支援的 104 職缺頁，或從推薦清單選取一筆。");
      root.innerHTML = message;
      return;
    }
    const verdict = verdictMeta(job);
    const offline = state.connected === false ? `<div class="notice is-offline" role="alert"><div class="notice-title">${icon("cloud")}無法連線到 localhost API</div><p>目前內容仍可閱讀；產生信件與更新投遞狀態暫不可用。</p></div>` : "";
    const pending = job.verdict === "pending_score" ? `<div class="notice is-warning" role="status"><div class="notice-title"><span class="spinner" aria-hidden="true"></span>條件篩選已通過</div><p>${state.context.budget_exhausted ? "今日評分額度已用盡，職缺會保留至隔日。" : "評分正在背景處理，完成後會自動更新。"}</p></div>` : "";
    const stale = [job.score_stale ? `評分使用 Profile ${shortRevision(job.score_profile_revision || job.evaluation_profile_revision)}` : "", job.letter_stale ? `求職信使用 Profile ${shortRevision(job.letter_profile_revision)}` : ""].filter(Boolean);
    const staleNotice = stale.length ? `<div class="notice is-warning" role="status"><div class="notice-title">${icon("info")}待更新的舊版結果</div><p>${escapeHTML(stale.join("；"))}。可從系統頁手動更新過時評分；既有投遞與信件歷史不會被改寫。</p></div>` : "";
    const hits = job.filter_hits?.length ? `<section class="card"><div class="card-heading"><h2>命中條件</h2><span>${job.filter_hits.length} 項</span></div><ul class="filter-hits">${job.filter_hits.map((hit) => `<li>${escapeHTML(hit)}</li>`).join("")}</ul></section>` : "";
    const total = job.score?.total ?? job.score_total;
    const reason = job.score?.reason || (job.verdict === "pending_score" ? "評分正在背景處理。" : job.filter_hits?.length ? "此職缺命中設定的排除條件。" : "尚無評分理由。");
    root.innerHTML = `
      <div class="section-stack">
        ${offline}${pending}${staleNotice}
        <div class="eyebrow-row"><span id="verdict-label" class="verdict-badge is-${verdict.tone}">${icon(verdict.icon)}${verdict.label}</span><span class="updated-at">${escapeHTML(job.source || "")}</span></div>
        <div class="job-hero"><div><h1 class="job-title">${escapeHTML(text(job.title))}</h1><p class="company-name">${escapeHTML(text(job.company_name))}</p>${scoreRevisionBadge(job)}</div>${total == null ? "" : `<div class="score-total"><strong>${escapeHTML(displayScore(total))}</strong><span>總分 / 100</span></div>`}</div>
        <p class="meta-line"><span class="meta-item">${icon("pin")}${escapeHTML(text(job.location))}</span><span class="meta-item">${icon("wallet")}${escapeHTML(salary(job))}</span></p>
        <section class="reason-card ${job.verdict === "unfit" ? "is-negative" : ""}"><strong>${job.verdict === "unfit" ? "排除理由" : job.verdict === "pending_score" ? "目前進度" : "判定理由"}</strong><p>${escapeHTML(reason)}</p></section>
        ${hits}${scoreCard(job)}
        <details class="card details-card"><summary>職缺內容摘要</summary><div class="details-content"><p id="description">${escapeHTML(text(job.description))}</p></div></details>
        ${letterCard(job)}
      </div>
      ${actionDock(job)}`;
    bindCurrentActions();
  }

  function renderQueue() {
    const root = document.querySelector("#screen-queue");
    const rows = state.queue.map((job) => `<a class="job-row" href="${escapeHTML(job.url)}" target="_blank" rel="noreferrer"><span class="job-row-copy"><strong>${escapeHTML(text(job.title))}</strong><span>${escapeHTML(text(job.company_name))}</span><span class="row-meta"><span>${escapeHTML(text(job.source))}</span><span>${escapeHTML(text(job.location))}</span><span>${escapeHTML(salary(job))}</span></span></span>${icon("arrow", "row-arrow")}</a>`).join("");
    root.innerHTML = `<div class="screen-heading"><div><h1>待看清單</h1><p>點開原始職缺後才能取得完整 JD 與評分。</p></div></div><div id="queue" class="job-list">${rows || emptyState("沒有待看職缺", "目前沒有需要補全文的職缺。")}</div>`;
  }

  function filterOptions() {
    return `<details class="card filter-panel" ${state.filtersOpen ? "open" : ""}><summary>進階篩選</summary><div class="filter-grid">
      <label>判定<select id="verdict"><option value="recommended">推薦</option><option value="not_recommended">不推薦</option><option value="pending_score">待評分</option><option value="pending_detail">待補全文</option><option value="unfit">不適合</option><option value="">全部</option></select></label>
      <label>處理狀態<select id="process"><option value="">全部</option><option value="discovered">待看</option><option value="new">新職缺</option><option value="queued">待評分</option><option value="scored">已評分</option><option value="shortlisted">已入選</option><option value="letter_requested">信件產生中</option><option value="letter_ready">信件就緒</option><option value="letter_failed">信件未過審</option><option value="filtered_out">已排除</option></select></label>
      <label>投遞狀態<select id="apply"><option value="">全部</option><option value="pending">待投遞</option><option value="applied">已投遞</option><option value="interview">面試</option><option value="offer">錄取</option><option value="ghosted">無回音</option><option value="dropped">放棄</option></select></label>
      <label>來源<select id="source-filter"><option value="">全部</option><option value="yourator">Yourator</option><option value="cake">Cake</option><option value="104">104</option></select></label>
    </div></details>`;
  }

  function renderShortlist() {
    const root = document.querySelector("#screen-shortlist");
    const rows = state.jobs.map((job) => {
      const viewed = state.viewedJobIDs.has(job.id);
      const viewedBadge = viewed ? `<span class="viewed-badge">${icon("check")}已看</span>` : "";
      return `<button class="job-row ${viewed ? "is-viewed" : ""}" type="button" data-job-id="${job.id}"><span class="row-score" aria-label="總分 ${displayScore(job.score_total)}">${displayScore(job.score_total)}</span><span class="job-row-copy"><strong>${escapeHTML(text(job.title))}</strong><span>${escapeHTML(text(job.company_name))}</span><span class="row-meta"><span>${escapeHTML(text(job.source))}</span><span>${escapeHTML(verdictMeta(job).label)}</span><span>${escapeHTML(text(job.apply_state))}</span>${viewedBadge}</span>${scoreRevisionBadge(job)}</span>${icon("arrow", "row-arrow")}</button>`;
    }).join("");
    const loadMore = state.jobsNextCursor ? `<button id="load-more-jobs" class="load-more-button" type="button" ${state.busy.has("jobs-page") ? "disabled" : ""}>${state.busy.has("jobs-page") ? "載入中…" : "載入更多"}</button>` : "";
    root.innerHTML = `<div class="screen-heading"><div><h1>推薦職缺</h1><p>依總分排序，逐筆決定是否產生求職信。</p></div></div>${filterOptions()}<div id="jobs" class="job-list">${rows || emptyState("沒有符合篩選的職缺", "調整進階篩選或稍後重新整理。")}</div>${loadMore}`;
    document.querySelector("#verdict").value = state.filters.verdict;
    document.querySelector("#process").value = state.filters.process;
    document.querySelector("#apply").value = state.filters.apply;
    document.querySelector("#source-filter").value = state.filters.source;
    const filterPanel = root.querySelector(".filter-panel");
    filterPanel.querySelector("summary").addEventListener("click", () => { state.filtersOpen = !filterPanel.open; });
    for (const id of ["verdict", "process", "apply", "source-filter"]) document.querySelector(`#${id}`).addEventListener("change", filtersChanged);
    root.querySelectorAll("[data-job-id]").forEach((button) => button.addEventListener("click", () => showJob(Number(button.dataset.jobId))));
    root.querySelector("#load-more-jobs")?.addEventListener("click", loadMoreJobs);
  }

  function runStats(run) {
    const stats = Object.entries(run.stats || {}).sort().map(([key, value]) => `${key}=${value}`);
    const verdicts = Object.entries(run.verdicts || {}).filter(([, value]) => value).sort().map(([key, value]) => `${key}=${value}`);
    return [...stats, ...verdicts];
  }

  const PROGRESS_STATES = [
    ["discovered", "待補全文"],
    ["new", "待條件篩選"],
    ["queued", "待評分"],
    ["letter_requested", "信件產生中"],
  ];

  const FAILURE_KINDS = {
    runner_error: "CLI 回報錯誤（額度、認證或逾時）",
    empty_output: "CLI 沒有輸出",
    no_json: "回應沒有 JSON 物件",
    invalid_json: "JSON 無法解析",
    reason_too_long: "理由超過 100 字上限",
    score_out_of_range: "分數超出 0–100",
    invalid_content: "回應未通過內容驗證",
  };

  function agentCallRow(call) {
    const at = String(call.created_at || "").replace("T", " ").slice(0, 19);
    const seconds = Math.round(Number(call.duration_ms || 0) / 100) / 10;
    const kind = call.ok ? "" : FAILURE_KINDS[call.failure_kind] || "未分類的失敗";
    const detail = call.ok ? "" : `<span class="run-detail"><strong>${escapeHTML(kind)}</strong><br>${escapeHTML(call.detail || "未記錄回應內容")}</span>`;
    return `<article class="run-item ${call.ok ? "" : "is-failed"}"><div class="run-heading"><strong>${escapeHTML(call.role)} · ${escapeHTML(call.runner)}</strong><span>${escapeHTML(at)}</span></div><div class="run-stats"><span>${call.ok ? "呼叫成功" : "呼叫未完成"}</span><span>職缺 ${escapeHTML(String(call.job_id ?? "—"))}</span><span>${escapeHTML(String(seconds))}s</span></div>${detail}</article>`;
  }

  function progressSection() {
    const progress = state.progress;
    if (!progress) return `<section aria-labelledby="progress-title"><div class="card-heading"><h2 id="progress-title">處理進度</h2></div>${emptyState("尚無處理進度", state.connected === false ? "連線恢復後會顯示待處理職缺與 Agent 呼叫。" : "重新整理後會顯示待處理職缺與 Agent 呼叫。")}</section>`;
    const counts = progress.jobs || {};
    const budget = progress.score_budget || {};
    const rows = PROGRESS_STATES.map(([key, label]) => `<div class="system-row"><span class="system-copy"><strong>${label}</strong><span>${escapeHTML(key)}</span></span><span class="metric-value">${Number(counts[key] || 0)}</span></div>`).join("");
    const budgetRow = budget.limited
      ? `<div class="system-row"><span class="system-copy"><strong>今日評分額度</strong><span>用盡後職缺保留至隔日</span></span><span class="metric-value ${Number(budget.remaining) === 0 ? "is-warning" : ""}">剩 ${Number(budget.remaining || 0)}</span></div>`
      : `<div class="system-row"><span class="system-copy"><strong>今日評分額度</strong><span>未設定上限</span></span><span class="metric-value">不限</span></div>`;
    const calls = (progress.agent_calls || []).map(agentCallRow).join("");
    return `<section class="card system-group" aria-labelledby="progress-title"><div class="card-heading"><h2 id="progress-title">處理進度</h2><span>常駐 worker 待消化的職缺</span></div><div class="system-card">${rows}${budgetRow}</div></section>
      <section aria-labelledby="agent-calls-title"><div class="card-heading"><h2 id="agent-calls-title">Agent 呼叫紀錄</h2><span>最近 20 筆</span></div><div id="agent-calls" class="run-list">${calls || emptyState("尚無 Agent 呼叫", "評分或求職信執行後會顯示在這裡。")}</div></section>`;
  }

  function renderSystem() {
    const root = document.querySelector("#screen-system");
    const runs = state.runs.map((run) => `<article class="run-item"><div class="run-heading"><strong>${escapeHTML(text(run.trigger))}</strong><span>${escapeHTML(text(run.started_at))}</span></div><div class="run-stats">${runStats(run).map((value) => `<span>${escapeHTML(value)}</span>`).join("")}${run.error ? `<span>${escapeHTML(run.error)}</span>` : ""}</div></article>`).join("");
    const profile = state.profile;
    const profileStatus = profile?.status || (state.connected === false ? "offline" : "loading");
    const summary = profile?.summary || {};
    const revision = profile?.profile_revision?.replace(/^sha256:/, "").slice(0, 8) || "—";
    const profileCopy = profileStatus === "ready"
      ? `年資 ${escapeHTML(text(summary.years_of_experience))} 年 · ${escapeHTML(text(summary.skill_count))} 項技能 · ${escapeHTML(text(summary.experience_count))} 段經歷<br>${escapeHTML(summary.directions?.join?.("、") || summary.direction || "方向未提供")} · revision ${escapeHTML(revision)}`
      : profileStatus === "invalid"
        ? `Profile 檔案需要人工修復。${profile.issues?.length ? `目前有 ${profile.issues.length} 項安全檢查問題。` : ""}`
        : profileStatus === "missing" ? "尚未建立履歷與求職條件。" : "目前無法讀取 Profile 狀態。";
    const profileAction = profileStatus === "missing" ? "開始設定" : profileStatus === "ready" ? "編輯履歷與求職條件" : profileStatus === "invalid" ? "查看修復說明" : "稍後再試";
    const runUnavailable = ["missing", "invalid", "degraded", "offline"].includes(profileStatus);
    const estimate = profile?.reprocess_estimate || {};
    const staleJobs = Number(estimate.partial_screened || 0) + Number(estimate.requeued || 0);
    const protectedJobs = Number(estimate.protected || 0);
    root.innerHTML = `<div class="section-stack"><div class="screen-heading"><div><h1>系統</h1><p>連線、Profile、批次與執行歷程。</p></div></div>
      <section class="card system-group" aria-labelledby="connection-title"><div class="card-heading"><h2 id="connection-title">連線與設定</h2></div><div class="system-card"><div class="system-row"><span class="system-copy"><strong>localhost API</strong><span>Side Panel 的 loopback 連線</span></span><span class="system-status ${state.connected ? "" : "is-warning"}"><span class="connection-dot"></span>${state.connected ? "正常" : "離線"}</span></div></div><button id="open-options" class="button is-secondary is-full" type="button">開啟連線設定</button></section>
      <section class="card profile-card" aria-labelledby="profile-title"><div class="card-heading"><h2 id="profile-title">Profile</h2><span class="profile-state is-${escapeHTML(profileStatus)}">${escapeHTML(profileStatus)}</span></div><p>${profileCopy}</p>${profileStatus === "ready" ? `<p class="reprocess-copy">${staleJobs ? `${staleJobs} 筆職缺使用舊版 Profile，等待手動更新。` : "所有可更新職缺均使用目前 Profile。"}${protectedJobs ? `另有 ${protectedJobs} 筆求職信歷史受保護。` : ""}</p>` : ""}<div class="button-stack"><button id="open-profile" class="button is-secondary is-full" type="button" ${profileStatus === "offline" || profileStatus === "loading" ? "disabled" : ""}>${escapeHTML(profileAction)}</button><button id="reprocess-profile" class="button is-primary is-full" type="button" ${profileStatus !== "ready" || staleJobs === 0 || state.busy.has("reprocess") ? "disabled" : ""}>${state.busy.has("reprocess") ? '<span class="spinner" aria-hidden="true"></span>正在排入更新' : `${icon("refresh")}更新過時評分職缺`}</button></div></section>
      <section class="card system-group" aria-labelledby="batch-title"><div class="card-heading"><h2 id="batch-title">自動與手動批次</h2></div><div class="system-card"><div class="system-row"><span class="system-copy"><strong>自動抓取時間</strong><span>每日 08:30（Asia/Taipei）</span></span><span class="metric-value">每日</span></div></div><button id="run" class="button is-primary is-full" type="button" ${state.connected === false || state.busy.has("run") || runUnavailable ? "disabled" : ""}>${state.busy.has("run") ? '<span class="spinner" aria-hidden="true"></span>抓取已開始' : `${icon("play")}立即手動抓取`}</button></section>
      ${progressSection()}
      <section aria-labelledby="runs-title"><div class="card-heading"><h2 id="runs-title">批次歷程</h2><span>只記抓取事實</span></div><div id="runs" class="run-list">${runs || emptyState("尚無執行記錄", "手動抓取或排程執行後會顯示在這裡。")}</div></section></div>`;
    document.querySelector("#run").addEventListener("click", startRun);
    document.querySelector("#open-profile")?.addEventListener("click", () => chrome.tabs.create({ url: chrome.runtime.getURL("profile/index.html") }));
    document.querySelector("#open-options")?.addEventListener("click", () => chrome.runtime.openOptionsPage());
    document.querySelector("#reprocess-profile")?.addEventListener("click", reprocessProfile);
  }

  function renderAll() {
    state.scrollPositions[state.activeTab] = window.scrollY;
    updateHeader();
    document.querySelector("#queue-count").textContent = state.queue.length;
    document.querySelector("#shortlist-count").textContent = state.jobs.length;
    renderCurrent();
    renderQueue();
    renderShortlist();
    renderSystem();
    switchTab(state.activeTab);
  }

  function queryString() {
    const params = new URLSearchParams();
    if (state.filters.verdict) params.set("verdict", state.filters.verdict);
    if (state.filters.process) params.set("process_state", state.filters.process);
    if (state.filters.apply) params.set("apply_state", state.filters.apply);
    if (state.filters.source) params.set("source", state.filters.source);
    return params.toString();
  }

  async function loadCollections() {
    const request = ++state.collectionRequest;
    const query = queryString();
    const [jobs, queue, runs, progress, profile] = await Promise.all([api(`/api/v1/jobs?${query}`), api("/api/v1/queue"), api("/api/v1/runs"), api("/api/v1/status"), chrome.runtime.sendMessage({ type: "profile-api", path: "/api/v1/profile", method: "GET" })]);
    if (request !== state.collectionRequest) return false;
    const connected = [jobs, queue, runs, progress, profile].some((result) => result?.ok);
    setConnection(connected);
    if (jobs?.ok) {
      state.jobs = jobs.data.items || [];
      state.jobsNextCursor = jobs.data.next_cursor || null;
    }
    if (queue?.ok) state.queue = queue.data.items || [];
    if (runs?.ok) state.runs = runs.data.items || [];
    state.progress = progress?.ok ? progress.data : null;
    state.profile = profile?.ok ? profile.data : null;
    if (!connected) announce(jobs?.error || queue?.error || runs?.error || "無法連線到 jobfinder API");
    return true;
  }

  async function loadCurrentPage() {
    const response = await extensionMessage({ type: "current-page" });
    if (response?.ok && response.context) state.context = response.context;
    else if (!state.currentJob) state.context = { kind: "unsupported", status: "unsupported" };
    if (state.context.job_id) {
      const job = await api(`/api/v1/jobs/${state.context.job_id}`);
      if (job?.ok) {
        state.currentJob = job.data;
        setConnection(true);
        startPollingIfNeeded();
      } else if (job) {
        setConnection(false);
      }
    }
  }

  async function load() {
    refresh.classList.add("is-spinning");
    await Promise.all([loadCollections(), loadCurrentPage()]);
    renderAll();
    refresh.classList.remove("is-spinning");
    if (state.connected) announce("已連線至 jobfinder API");
  }

  async function showJob(id) {
    const result = await api(`/api/v1/jobs/${id}`);
    if (!result?.ok) return showToast(result?.error || "無法載入職缺");
    state.viewedJobIDs.add(id);
    state.currentJob = result.data;
    state.context = { kind: "selection", source: result.data.source, status: "selected" };
    renderAll();
    state.scrollPositions.current = 0;
    switchTab("current");
    startPollingIfNeeded();
  }

  function filtersChanged() {
    state.filters.verdict = document.querySelector("#verdict").value;
    state.filters.process = document.querySelector("#process").value;
    state.filters.apply = document.querySelector("#apply").value;
    state.filters.source = document.querySelector("#source-filter").value;
    loadCollections().then(() => {
      renderAll();
      state.scrollPositions.shortlist = 0;
      if (state.activeTab === "shortlist") window.scrollTo({ top: 0, behavior: "auto" });
    });
  }

  async function loadMoreJobs() {
    if (!state.jobsNextCursor || state.busy.has("jobs-page")) return;
    const request = state.collectionRequest;
    const cursor = state.jobsNextCursor;
    state.busy.add("jobs-page");
    renderShortlist();
    const params = new URLSearchParams(queryString());
    params.set("cursor", cursor);
    const result = await api(`/api/v1/jobs?${params}`);
    state.busy.delete("jobs-page");
    if (request !== state.collectionRequest || cursor !== state.jobsNextCursor) {
      renderAll();
      return;
    }
    if (!result?.ok) {
      renderShortlist();
      return showToast(result?.error || "無法載入更多職缺");
    }
    const known = new Set(state.jobs.map((job) => job.id));
    for (const job of result.data.items || []) {
      if (!known.has(job.id)) state.jobs.push(job);
    }
    state.jobsNextCursor = result.data.next_cursor || null;
    renderAll();
  }

  function bindCurrentActions() {
    document.querySelector("#copy")?.addEventListener("click", copyLetter);
    document.querySelector("#request-letter")?.addEventListener("click", requestLetter);
    document.querySelector("#rescore")?.addEventListener("click", rescoreJob);
    document.querySelector("#save-apply")?.addEventListener("click", saveApply);
    document.querySelector("#next-job")?.addEventListener("click", () => switchTab("queue"));
  }

  async function copyLetter() {
    try {
      await navigator.clipboard.writeText(state.currentJob.letter.content);
      showToast("已複製信件");
    } catch (_) {
      showToast("無法使用剪貼簿，信件文字仍可選取複製。");
    }
  }

  async function requestLetter() {
    if (!state.currentJob || state.busy.has("letter")) return;
    state.busy.add("letter");
    renderAll();
    const result = await api(`/api/v1/jobs/${state.currentJob.id}/letter`, "POST");
    state.busy.delete("letter");
    if (!result?.ok) {
      renderAll();
      return showToast(result?.error || "求職信要求失敗");
    }
    state.currentJob = result.data.job;
    renderAll();
    showToast("已受理求職信生成要求");
  }

  // rescoreJob redoes one job's score without touching the rest, so a single
  // wrong result costs one Agent call instead of a full reprocess.
  async function rescoreJob() {
    if (!state.currentJob || state.busy.has("rescore")) return;
    state.busy.add("rescore");
    renderAll();
    const result = await api(`/api/v1/jobs/${state.currentJob.id}/rescore`, "POST");
    state.busy.delete("rescore");
    if (!result?.ok) {
      renderAll();
      return showToast(result?.error || "無法重新評分");
    }
    state.currentJob = result.data.job;
    await loadCollections();
    renderAll();
    startPollingIfNeeded();
    showToast("已排入重新評分");
  }

  async function saveApply() {
    if (!state.currentJob || state.busy.has("apply")) return;
    const value = document.querySelector("#apply-state").value;
    state.busy.add("apply");
    renderAll();
    const result = await api(`/api/v1/jobs/${state.currentJob.id}/apply`, "POST", { apply_state: value });
    state.busy.delete("apply");
    if (!result?.ok) {
      renderAll();
      return showToast(result?.error || "投遞狀態更新失敗");
    }
    state.currentJob = result.data;
    await loadCollections();
    renderAll();
    showToast("投遞狀態已更新");
  }

  async function startRun() {
    if (state.busy.has("run")) return;
    state.busy.add("run");
    renderAll();
    const result = await api("/api/v1/runs", "POST");
    state.busy.delete("run");
    if (!result?.ok) {
      renderAll();
      return showToast(result?.error || "無法開始抓取");
    }
    await loadCollections();
    renderAll();
    showToast(result.data.status === "started" ? "已開始抓取" : "已有執行中的抓取");
  }

  async function reprocessProfile() {
    if (state.busy.has("reprocess")) return;
    state.busy.add("reprocess");
    renderAll();
    const result = await chrome.runtime.sendMessage({ type: "profile-api", path: "/api/v1/profile/reprocess", method: "POST" });
    state.busy.delete("reprocess");
    if (!result?.ok) {
      renderAll();
      return showToast(result?.error || "無法更新過時評分");
    }
    await loadCollections();
    renderAll();
    const queued = Number(result.data?.activation?.requeued || 0);
    showToast(queued ? `已排入 ${queued} 筆重新評分` : "過時職缺已更新");
  }

  function startPollingIfNeeded() {
    clearTimeout(state.pollTimer);
    if (state.currentJob?.verdict !== "pending_score" || state.context.budget_exhausted) return;
    state.pollUntil = Date.now() + POLL_LIMIT_MS;
    state.pollTimer = setTimeout(pollCurrent, POLL_INTERVAL_MS);
  }

  async function pollCurrent() {
    if (!state.currentJob || state.currentJob.verdict !== "pending_score") return;
    if (Date.now() >= state.pollUntil) {
      showToast("仍在處理，可稍後重新整理");
      return;
    }
    const result = await api(`/api/v1/jobs/${state.currentJob.id}`);
    if (result?.ok) {
      state.currentJob = result.data;
      renderAll();
      if (state.currentJob.verdict !== "pending_score") return;
    }
    state.pollTimer = setTimeout(pollCurrent, POLL_INTERVAL_MS);
  }

  tabs.forEach((tab) => {
    tab.addEventListener("click", () => switchTab(tab.dataset.tab));
    tab.addEventListener("keydown", (event) => {
      if (!["ArrowLeft", "ArrowRight"].includes(event.key)) return;
      event.preventDefault();
      const index = tabs.indexOf(tab);
      const next = event.key === "ArrowRight" ? (index + 1) % tabs.length : (index - 1 + tabs.length) % tabs.length;
      switchTab(tabs[next].dataset.tab);
      tabs[next].focus();
    });
  });

  refresh.addEventListener("click", load);
  themeToggle.addEventListener("click", async () => {
    state.theme = state.theme === "dark" ? "light" : "dark";
    applyTheme();
    await storageSet({ theme: state.theme });
    showToast(state.theme === "dark" ? "已切換至深色模式" : "已切換至淺色模式");
  });

  chrome.runtime.onMessage?.addListener((message) => {
    if (message?.type !== "page-context-updated") return;
    state.context = message.context;
    loadCurrentPage().then(renderAll);
  });

  storageGet({ theme: "light" }).then((settings) => {
    state.theme = settings.theme === "dark" ? "dark" : "light";
    applyTheme();
    load();
  });
})();
