(() => {
  const mock = window.JOBFINDER_MOCK;
  const state = {
    activeTab: "current",
    activeState: mock.activeState,
    theme: new URLSearchParams(window.location.search).get("theme") === "dark" ? "dark" : "light",
    toastTimer: null,
  };

  const screens = [...document.querySelectorAll("[data-screen]")];
  const tabs = [...document.querySelectorAll("[data-tab]")];
  const context = document.querySelector("#page-context");
  const connection = document.querySelector("#connection");
  const themeToggle = document.querySelector("#theme-toggle");
  const refresh = document.querySelector("#refresh");
  const toast = document.querySelector("#toast");

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
    moon: '<path d="M20 15.2A8.2 8.2 0 0 1 8.8 4 8.5 8.5 0 1 0 20 15.2Z" />',
    sun: '<circle cx="12" cy="12" r="3.5" /><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" />',
  };

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

  function currentJob() {
    return mock.states[state.activeState];
  }

  function applyTheme() {
    const dark = state.theme === "dark";
    document.documentElement.dataset.theme = state.theme;
    themeToggle.innerHTML = icon(dark ? "sun" : "moon");
    themeToggle.setAttribute("aria-label", dark ? "切換至淺色模式" : "切換至深色模式");
    themeToggle.setAttribute("aria-pressed", String(dark));
  }

  function verdictMeta(job) {
    const values = {
      recommended: { label: "推薦", tone: "positive", icon: "check" },
      not_recommended: { label: "不推薦", tone: "neutral", icon: "close" },
      unfit: { label: "不適合", tone: "negative", icon: "close" },
      pending_score: { label: "評分中", tone: "warning", icon: "clock" },
      pending_detail: { label: "待補全文", tone: "warning", icon: "clock" },
    };
    return values[job.verdict] || { label: "等待判定", tone: "neutral", icon: "clock" };
  }

  function updateHeader() {
    const job = currentJob();
    const isOffline = Boolean(job.offline);
    connection.classList.toggle("is-offline", isOffline);
    connection.lastElementChild.textContent = isOffline ? "離線" : "已連線";
    context.innerHTML = `
      <span class="source-badge">${escapeHTML(job.source)}</span>
      <span class="context-copy">
        <strong>${escapeHTML(job.title)}</strong>
        <span>${escapeHTML(job.company)}・${escapeHTML(job.updated)}</span>
      </span>
      <span class="capture-badge">${icon(isOffline ? "clock" : "check")}<span>${isOffline ? "快取" : "已擷取"}</span></span>
    `;
  }

  function scoreCard(job) {
    if (!job.dimensions?.length) return "";
    const rows = job.dimensions.map(([label, score]) => `
      <div class="score-row">
        <div class="score-row-label"><span>${escapeHTML(label)}</span><strong>${score}</strong></div>
        <div class="score-track" role="progressbar" aria-label="${escapeHTML(label)}評分" aria-valuenow="${score}" aria-valuemin="0" aria-valuemax="100">
          <span style="--score: ${score}%"></span>
        </div>
      </div>
    `).join("");
    const tags = job.highlights.map((tag) => `<span class="tag">${escapeHTML(tag)}</span>`).join("");
    return `
      <section class="card" aria-labelledby="score-title">
        <div class="card-heading"><h2 id="score-title">五維評分</h2><span>0–100</span></div>
        <div class="score-list">${rows}</div>
        <div class="tag-list" aria-label="職缺重點">${tags}</div>
      </section>
    `;
  }

  function letterCard(job) {
    if (job.letterState === "unavailable") return "";
    if (job.letterState === "ready") {
      return `
        <section class="card letter-card" aria-labelledby="letter-title">
          <div class="letter-heading">
            <h2 id="letter-title">求職信</h2>
            <span class="letter-state is-ready">${icon("check")}已過審</span>
          </div>
          <pre id="letter-content" class="letter-content">${escapeHTML(job.letter)}</pre>
          <div class="select-row">
            <label for="apply-state">投遞狀態</label>
            <select id="apply-state">
              <option value="pending" selected>待投遞</option>
              <option value="applied">已投遞</option>
              <option value="interview">面試</option>
              <option value="offer">錄取</option>
              <option value="ghosted">無回音</option>
              <option value="dropped">放棄</option>
            </select>
          </div>
        </section>
      `;
    }
    if (job.letterState === "requested") {
      return `
        <section class="card letter-card" aria-labelledby="letter-title">
          <div class="letter-heading">
            <h2 id="letter-title">求職信</h2>
            <span class="letter-state is-pending"><span class="spinner" aria-hidden="true"></span>產生中</span>
          </div>
          <p class="letter-copy">要求已送出。常駐 worker 完成起草與審查後，這裡會顯示可複製的信件。</p>
        </section>
      `;
    }
    return `
      <section class="card letter-card" aria-labelledby="letter-title">
        <div class="letter-heading">
          <h2 id="letter-title">求職信</h2>
          <span class="letter-state">按需生成</span>
        </div>
        <p class="letter-copy">確認想投遞後才產生，避免為尚未決定的職缺消耗 Agent 額度。</p>
      </section>
    `;
  }

  function actionDock(job) {
    const secondary = `<button class="button is-secondary" type="button" data-action="open-source" aria-label="開啟原始職缺">${icon("external")}</button>`;
    if (job.offline) {
      return `<div class="action-dock">${secondary}<button class="button is-primary" type="button" disabled>${icon("cloud")}等待重新連線</button></div>`;
    }
    if (job.letterState === "ready") {
      return `<div class="action-dock">${secondary}<button class="button is-primary" type="button" data-action="copy-letter">${icon("copy")}複製求職信</button></div>`;
    }
    if (job.letterState === "requested") {
      return `<div class="action-dock">${secondary}<button class="button is-primary" type="button" disabled><span class="spinner" aria-hidden="true"></span>求職信產生中</button></div>`;
    }
    if (job.verdict === "recommended") {
      return `<div class="action-dock">${secondary}<button class="button is-primary" type="button" data-action="request-letter">${icon("spark")}產生求職信</button></div>`;
    }
    if (job.verdict === "pending_score") {
      return `<div class="action-dock">${secondary}<button class="button is-primary" type="button" disabled><span class="spinner" aria-hidden="true"></span>正在評分</button></div>`;
    }
    return `<div class="action-dock">${secondary}<button class="button is-primary" type="button" data-action="next-job">開下一筆${icon("arrow")}</button></div>`;
  }

  function renderCurrent() {
    const root = document.querySelector("#screen-current");
    const job = currentJob();
    const verdict = verdictMeta(job);
    const offline = job.offline ? `
      <div class="notice is-offline" role="alert">
        <div class="notice-title">${icon("cloud")}無法連線到 localhost API</div>
        <p>目前顯示最近一次快取；產生信件與更新投遞狀態暫不可用。</p>
      </div>
    ` : "";
    const pending = job.verdict === "pending_score" ? `
      <div class="notice is-warning" role="status">
        <div class="notice-title"><span class="spinner" aria-hidden="true"></span>條件篩選已通過</div>
        <p>評分正在背景處理。你可以繼續閱讀職缺，結果完成後會自動更新。</p>
      </div>
    ` : "";
    const hits = job.filterHits?.length ? `
      <section class="card" aria-labelledby="filter-title">
        <div class="card-heading"><h2 id="filter-title">命中條件</h2><span>${job.filterHits.length} 項</span></div>
        <ul class="filter-hits">${job.filterHits.map((hit) => `<li>${escapeHTML(hit)}</li>`).join("")}</ul>
      </section>
    ` : "";
    const score = job.total == null ? "" : `<div class="score-total"><strong>${job.total}</strong><span>總分 / 100</span></div>`;
    root.innerHTML = `
      <div class="section-stack">
        ${offline}
        ${pending}
        <div class="eyebrow-row">
          <span class="verdict-badge is-${verdict.tone}">${icon(verdict.icon)}${verdict.label}</span>
          <span class="updated-at">${escapeHTML(job.updated)}</span>
        </div>
        <div class="job-hero">
          <div>
            <h1 class="job-title">${escapeHTML(job.title)}</h1>
            <p class="company-name">${escapeHTML(job.company)}</p>
          </div>
          ${score}
        </div>
        <p class="meta-line">
          <span class="meta-item">${icon("pin")}${escapeHTML(job.location)}</span>
          <span class="meta-item">${icon("wallet")}${escapeHTML(job.salary)}</span>
        </p>
        <section class="reason-card ${job.verdict === "unfit" ? "is-negative" : ""}">
          <strong>${job.verdict === "unfit" ? "排除理由" : job.verdict === "pending_score" ? "目前進度" : "推薦理由"}</strong>
          <p>${escapeHTML(job.reason)}</p>
        </section>
        ${hits}
        ${scoreCard(job)}
        <details class="card details-card">
          <summary>職缺內容摘要</summary>
          <div class="details-content"><p>${escapeHTML(job.description)}</p></div>
        </details>
        ${letterCard(job)}
      </div>
      ${actionDock(job)}
    `;
  }

  function renderQueue() {
    const root = document.querySelector("#screen-queue");
    const rows = mock.queue.map((job) => `
      <button class="job-row" type="button" data-queue-id="${escapeHTML(job.id)}">
        <span class="job-row-copy">
          <strong>${escapeHTML(job.title)}</strong>
          <span>${escapeHTML(job.company)}</span>
          <span class="row-meta"><span>${escapeHTML(job.source)}</span><span>${escapeHTML(job.location)}</span><span>${escapeHTML(job.salary)}</span></span>
        </span>
        ${icon("arrow", "row-arrow")}
      </button>
    `).join("");
    root.innerHTML = `
      <div class="screen-heading">
        <div><h1>待看清單</h1><p>點開原始職缺後才能取得完整 JD 與評分。</p></div>
        <span class="progress-ring" aria-label="3 筆待看職缺"></span>
      </div>
      <div class="job-list">${rows}</div>
    `;
  }

  function renderShortlist() {
    const root = document.querySelector("#screen-shortlist");
    const rows = mock.shortlist.map((job) => `
      <button class="job-row" type="button" data-shortlist-state="${escapeHTML(job.state)}">
        <span class="row-score" aria-label="總分 ${job.score}">${job.score}</span>
        <span class="job-row-copy">
          <strong>${escapeHTML(job.title)}</strong>
          <span>${escapeHTML(job.company)}</span>
          <span class="row-meta"><span>${escapeHTML(job.source)}</span><span>${escapeHTML(job.letter)}</span><span>${escapeHTML(job.apply)}</span></span>
        </span>
        ${icon("arrow", "row-arrow")}
      </button>
    `).join("");
    root.innerHTML = `
      <div class="screen-heading">
        <div><h1>推薦職缺</h1><p>依總分排序，逐筆決定是否產生求職信。</p></div>
      </div>
      <div class="job-list">${rows}</div>
    `;
  }

  function renderSystem() {
    const root = document.querySelector("#screen-system");
    const job = currentJob();
    const runs = mock.runs.map((run) => `
      <article class="run-item">
        <div class="run-heading"><strong>${escapeHTML(run.trigger)}</strong><span>${escapeHTML(run.time)}</span></div>
        <div class="run-stats"><span>抓取 ${run.fetched}</span><span>新增 ${run.created}</span><span>錯誤 ${escapeHTML(run.error)}</span></div>
      </article>
    `).join("");
    const controls = [
      ["recommended", "推薦"],
      ["pending", "評分中"],
      ["unfit", "不適合"],
      ["letterReady", "信件就緒"],
      ["offline", "API 離線"],
    ].map(([key, label]) => `<button class="prototype-button ${state.activeState === key ? "is-active" : ""}" type="button" data-preview-state="${key}">${label}</button>`).join("");
    root.innerHTML = `
      <div class="section-stack">
        <div class="screen-heading"><div><h1>系統</h1><p>連線、背景處理與最近抓取狀態。</p></div></div>
        <section class="card system-card" aria-label="系統狀態">
          <div class="system-row">
            <span class="system-copy"><strong>localhost API</strong><span>127.0.0.1:8686</span></span>
            <span class="system-status ${job.offline ? "is-warning" : ""}"><span class="connection-dot"></span>${job.offline ? "離線" : "正常"}</span>
          </div>
          <div class="system-row">
            <span class="system-copy"><strong>常駐 worker</strong><span>等待下一筆工作</span></span>
            <span class="system-status"><span class="connection-dot"></span>運作中</span>
          </div>
          <div class="system-row">
            <span class="system-copy"><strong>每日自動抓取</strong><span>${escapeHTML(mock.schedule.timezone)}</span></span>
            <span class="metric-value">${escapeHTML(mock.schedule.time)}</span>
          </div>
        </section>
        <button id="manual-fetch" class="button is-primary is-full" type="button">${icon("play")}手動抓取自動來源</button>
        <section aria-labelledby="runs-title">
          <div class="card-heading"><h2 id="runs-title">最近執行</h2><span>只記抓取事實</span></div>
          <div class="run-list">${runs}</div>
        </section>
        <section class="card prototype-panel" aria-labelledby="prototype-title">
          <div class="card-heading"><h2 id="prototype-title">Prototype 狀態</h2><span>設計稿專用</span></div>
          <div class="prototype-controls">${controls}</div>
        </section>
      </div>
    `;
  }

  function renderAll() {
    updateHeader();
    document.querySelector("#queue-count").textContent = mock.queue.length;
    document.querySelector("#shortlist-count").textContent = mock.shortlist.length;
    renderCurrent();
    renderQueue();
    renderShortlist();
    renderSystem();
    bindDynamicActions();
  }

  function switchTab(name) {
    state.activeTab = name;
    tabs.forEach((tab) => {
      const active = tab.dataset.tab === name;
      tab.classList.toggle("is-active", active);
      tab.setAttribute("aria-selected", String(active));
    });
    screens.forEach((screen) => {
      screen.hidden = screen.dataset.screen !== name;
    });
    window.scrollTo({ top: 0, behavior: "auto" });
  }

  function showToast(message) {
    clearTimeout(state.toastTimer);
    toast.textContent = message;
    toast.hidden = false;
    state.toastTimer = setTimeout(() => {
      toast.hidden = true;
    }, 2600);
  }

  function previewState(name) {
    state.activeState = name;
    renderAll();
    switchTab("current");
    showToast(`已切換為「${document.querySelector(`[data-preview-state="${name}"]`)?.textContent || name}」預覽`);
  }

  function bindDynamicActions() {
    document.querySelectorAll("[data-action]").forEach((button) => {
      button.addEventListener("click", async () => {
        const action = button.dataset.action;
        const job = currentJob();
        if (action === "request-letter") {
          job.letterState = "requested";
          renderAll();
          switchTab("current");
          showToast("已送出求職信生成要求");
        }
        if (action === "open-source") showToast("正式版會由使用者動作開啟原始職缺");
        if (action === "next-job") {
          switchTab("queue");
          showToast("請從待看清單選擇下一筆職缺");
        }
        if (action === "copy-letter") {
          const letter = job.letter || "";
          try {
            await navigator.clipboard.writeText(letter);
            showToast("求職信已複製");
          } catch (_) {
            showToast("瀏覽器未允許剪貼簿；信件文字仍可手動選取");
          }
        }
      });
    });

    document.querySelectorAll("[data-queue-id]").forEach((button) => {
      button.addEventListener("click", () => showToast("正式版會以使用者動作開啟這筆原始職缺"));
    });

    document.querySelectorAll("[data-shortlist-state]").forEach((button) => {
      button.addEventListener("click", () => {
        state.activeState = button.dataset.shortlistState;
        renderAll();
        switchTab("current");
      });
    });

    document.querySelectorAll("[data-preview-state]").forEach((button) => {
      button.addEventListener("click", () => previewState(button.dataset.previewState));
    });

    const manualFetch = document.querySelector("#manual-fetch");
    if (manualFetch) {
      manualFetch.addEventListener("click", () => {
        manualFetch.disabled = true;
        manualFetch.innerHTML = '<span class="spinner" aria-hidden="true"></span>抓取已開始';
        showToast("已觸發自動來源抓取");
        setTimeout(() => {
          manualFetch.disabled = false;
          manualFetch.innerHTML = `${icon("play")}手動抓取自動來源`;
        }, 1600);
      });
    }
  }

  tabs.forEach((tab) => tab.addEventListener("click", () => switchTab(tab.dataset.tab)));

  refresh.addEventListener("click", () => {
    refresh.classList.add("is-spinning");
    showToast("已重新整理目前頁面狀態");
    setTimeout(() => refresh.classList.remove("is-spinning"), 720);
  });

  themeToggle.addEventListener("click", () => {
    state.theme = state.theme === "dark" ? "light" : "dark";
    applyTheme();
    renderAll();
    switchTab(state.activeTab);
    showToast(state.theme === "dark" ? "已切換至深色模式" : "已切換至淺色模式");
  });

  applyTheme();
  renderAll();
})();
