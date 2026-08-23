(() => {
  const POLL_INTERVAL_MS = 3000;
  const POLL_LIMIT_MS = 5 * 60 * 1000;
  const VERDICTS = {
    unfit: { label: "不適合", tone: "negative", icon: "close" },
    recommended: { label: "推薦", tone: "positive", icon: "check" },
    not_recommended: { label: "不推薦", tone: "neutral", icon: "close" },
    pending_detail: { label: "待看", tone: "warning", icon: "clock" },
    pending_screen: { label: "篩選中", tone: "warning", icon: "clock" },
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
    queueNextCursor: null,
    runs: [],
    duplicates: [],
    progress: null,
    profile: null,
    settings: null,
    // pushedJobIDs are the jobs the user sent through immediately. The server
    // does not distinguish "waiting" from "being worked on", so the button that
    // was pressed is what turns the action into a progress state here.
    pushedJobIDs: new Set(),
    // letterHistory belongs to one job and is fetched only when the user opens
    // the section: it carries every draft in full, which is far more than the
    // job view should drag along on every render.
    letterHistory: { jobID: null, attempts: null, open: false },
    filters: { verdict: "recommended", process: "", apply: "", source: "" },
    filtersOpen: false,
    scrollPositions: { current: 0, queue: 0, shortlist: 0, system: 0 },
    collectionRequest: 0,
    busy: new Set(),
    toastTimer: null,
    pollTimer: null,
    pollUntil: 0,
    activityTimer: null,
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

  // Screening and scoring are versioned apart, so each badge names its own gate:
  // a job can be screened under the current rules and still hold an old score.
  function revisionBadges(job) {
    const badges = [];
    if (job.filter_result_revision) {
      badges.push(`<span class="revision-badge ${job.filter_stale ? "is-stale" : "is-current"}">篩選 ${escapeHTML(shortRevision(job.filter_result_revision))} · ${job.filter_stale ? "待重篩" : "最新"}</span>`);
    }
    const total = job.score?.total ?? job.score_total;
    if (total != null) {
      badges.push(`<span class="revision-badge ${job.score_stale ? "is-stale" : "is-current"}">評分 ${escapeHTML(shortRevision(job.score_result_revision))} · ${job.score_stale ? "待重評" : "最新"}</span>`);
    }
    return badges.join("");
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
    scheduleActivityPoll();
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
      ["工作內容", job.score.content_fit], ["待遇與制度", job.score.benefit_fit],
      ["加分條件", job.score.bonus_fit], ["產業", job.score.industry_fit],
    ];
    const rows = dimensions.map(([label, value]) => `
      <div class="score-row">
        <div class="score-row-label"><span>${label}</span><strong>${displayScore(value)}</strong></div>
        <div class="score-track" role="progressbar" aria-label="${label}評分" aria-valuenow="${Number(value) || 0}" aria-valuemin="0" aria-valuemax="100"><span style="--score: ${Number(value) || 0}%"></span></div>
      </div>`).join("");
    return `<section class="card" aria-labelledby="score-title"><div class="card-heading"><h2 id="score-title">四維評分</h2><span>0–100</span></div><div id="score" class="score-list" aria-label="總分 ${text(job.score.total)}">${rows}</div></section>`;
  }

  function trackingFields(job) {
    return `
      <div class="select-row"><label for="apply-state">投遞狀態</label><select id="apply-state">
        ${[["pending", "待投遞"], ["applied", "已投遞"], ["interview", "面試"], ["offer", "錄取"], ["ghosted", "無回音"], ["dropped", "放棄"]].map(([value, label]) => `<option value="${value}" ${job.apply_state === value ? "selected" : ""}>${label}</option>`).join("")}
      </select></div>
      <button id="save-apply" class="button is-secondary is-full" type="button" ${state.connected === false || state.busy.has("apply") ? "disabled" : ""}>更新投遞狀態</button>
      <details><summary class="helper-text">狀態記錄</summary><div id="events" class="event-list">${(job.status_events || []).map((event) => `<p>${escapeHTML(event.axis)}：${escapeHTML(event.from_state || "—")} → ${escapeHTML(event.to_state)}</p>`).join("") || "<p>尚無狀態記錄</p>"}</div></details>`;
  }

  const SOURCE_LABELS = { "104": "104", cake: "Cake", yourator: "Yourator" };

  // groupCard lists the other platforms the same listing appeared on, so the user
  // can pick where to apply. Assessment and letter exist once per group: the other
  // members are links, not separate jobs.
  function groupCard(job) {
    const group = job.group;
    if (!group || (group.members || []).length < 2) return "";
    const rows = group.members
      .filter((member) => member.job_id !== job.id)
      .map((member) => `<div class="system-row"><span class="system-copy"><strong>${escapeHTML(SOURCE_LABELS[member.source] || member.source)}</strong><span>${escapeHTML(member.external_id)}</span></span><span class="dupe-actions"><a class="button is-secondary" href="${escapeHTML(member.url)}" target="_blank" rel="noreferrer">${icon("external")}開啟</a><button class="button is-secondary" type="button" data-unmerge="${Number(member.job_id)}" ${state.connected === false || state.busy.has("unmerge") ? "disabled" : ""}>取消合併</button></span></div>`)
      .join("");
    if (!rows) return "";
    return `<section class="card system-group" aria-labelledby="group-title"><div class="card-heading"><h2 id="group-title">其他來源</h2><span>同一職缺的重複刊登</span></div><div class="system-card">${rows}</div><p class="helper-text">評分與求職信只保留一份。若其實是不同職缺，取消合併會還原該筆的原本狀態。</p></section>`;
  }

  function letterCard(job) {
    // A finalized letter is the last round's version, produced from every review
    // so far but never sent back for a final verdict. It is a real letter and is
    // shown as one, flagged so the user gives it one read before applying.
    const finalized = job.letter?.status === "finalized";
    const letterReady = job.letter_state === "ready" && (job.letter?.status === "approved" || finalized);
    const requested = job.letter_state === "requested";
    const failed = job.letter_state === "failed";
    if (!job.letter_state) return `<section class="card"><div class="letter-heading"><h2>投遞追蹤</h2></div>${trackingFields(job)}</section>`;
    let body = "";
    let badge = "按需生成";
    if (letterReady) {
      badge = finalized ? `${icon("check")}已達輪數上限` : `${icon("check")}已過審`;
      const note = finalized ? '<p class="letter-copy">這是修改輪數用完後的最終版，未再經過一次審查。投遞前請自行過目。</p>' : "";
      body = `${note}<pre id="letter" class="letter-content">${escapeHTML(job.letter.content)}</pre>`;
    } else if (requested) {
      badge = '<span class="spinner" aria-hidden="true"></span>產生中';
      body = '<p class="letter-copy">要求已送出。常駐 worker 完成起草與審查後，重新整理即可取得信件。</p><pre id="letter" class="visually-hidden"></pre>';
    } else {
      body = `<p class="letter-copy">${failed ? "上次產製失敗，可再次要求產生；失敗原因見系統頁的 Agent 呼叫紀錄。" : "確認想投遞後才產生，避免消耗 Agent 額度。"}</p><pre id="letter" class="visually-hidden"></pre>`;
    }
    return `
      <section class="card letter-card" aria-labelledby="letter-title">
        <div class="letter-heading"><h2 id="letter-title">求職信</h2><span id="letter-state" class="letter-state ${letterReady ? "is-ready" : requested ? "is-pending" : ""}">${badge}</span></div>
        ${body}
        ${letterHistoryBlock(job)}
        ${trackingFields(job)}
      </section>`;
  }

  // A finalized generation carries no chip of its own: the letter card above the
  // history already says the letter is the last round's version, and repeating it
  // on every entry says the same thing twice on one screen.
  const ATTEMPT_STATUS = { approved: "已過審", failed: "未產出信件", running: "產生中" };
  const CALL_ROLE = { drafter: "起草", reviewer: "審查" };

  // letterHistoryBlock is the only place a past round is readable. It stays shut
  // until asked: a generation holds several full drafts, and the answer most
  // readings want is the final letter above it.
  function letterHistoryBlock(job) {
    const history = state.letterHistory;
    const open = history.open && history.jobID === job.id;
    if (!open) {
      return `<details class="details-card letter-history"><summary data-letter-history="${job.id}">產製歷程</summary></details>`;
    }
    if (!history.attempts) {
      return `<details class="details-card letter-history" open><summary data-letter-history="${job.id}">產製歷程</summary><div class="details-content"><p class="letter-copy"><span class="spinner" aria-hidden="true"></span>讀取中…</p></div></details>`;
    }
    if (!history.attempts.length) {
      return `<details class="details-card letter-history" open><summary data-letter-history="${job.id}">產製歷程</summary><div class="details-content"><p class="letter-copy">這筆職缺還沒有跑過求職信產製。</p></div></details>`;
    }
    const entries = history.attempts.map(attemptEntry).join("");
    return `<details class="details-card letter-history" open><summary data-letter-history="${job.id}">產製歷程<span class="attempt-count">${history.attempts.length} 次</span></summary><div class="details-content">${entries}</div></details>`;
  }

  function attemptEntry(attempt) {
    const status = attempt.status === "finalized" ? "" : ATTEMPT_STATUS[attempt.status] || attempt.status;
    const when = attempt.started_at ? new Date(attempt.started_at).toLocaleString("zh-TW", { hour12: false }) : "";
    const runners = [attempt.runner_draft && `起草 ${attempt.runner_draft}`, attempt.runner_review && `審查 ${attempt.runner_review}`].filter(Boolean).join("・");
    const chip = status ? `<span class="attempt-status is-${escapeHTML(attempt.status)}">${escapeHTML(status)}</span>` : "";
    return `<details class="attempt"><summary>${chip}<span>${escapeHTML(when)}</span><span>${attempt.rounds} 輪</span><span>${escapeHTML(runners)}</span></summary><div class="attempt-body">${attemptRounds(attempt)}</div></details>`;
  }

  // A generation reads as the argument it was: each round is the letter that
  // round's drafter wrote followed by what was said about it, and the last round
  // ends with the reason the argument stopped. The letter this generation
  // produced closes the entry, because it is not always the last round's draft:
  // an approving reviewer may hand back an edited version, and that edit is what
  // was saved.
  function attemptRounds(attempt) {
    const outcomes = (attempt.review_log || "").split("\n").filter(Boolean);
    const calls = attempt.calls || [];
    const total = Math.max(outcomes.length, attempt.rounds || 0, ...calls.map((call) => call.round || 0));
    if (!total) return `<p class="letter-copy">${attempt.status === "running" ? "產製進行中，完成後這裡會列出每一輪。" : "這次產製沒有留下任何一輪的紀錄。"}</p>`;
    const blocks = [];
    for (let round = 1; round <= total; round += 1) {
      blocks.push(`<div class="attempt-round-block"><div class="attempt-round-head"><span class="attempt-round">第 ${round} 輪</span>${roundRunners(calls, round)}</div>${roundLetter(calls, round)}${roundOutcome(outcomes[round - 1], calls, round)}</div>`);
    }
    if (attempt.content) {
      const edited = attempt.content !== lastDraft(calls, total);
      blocks.push(`<div class="attempt-round-block"><div class="attempt-round-head"><span class="attempt-round">這次產出的信件</span>${edited ? "<span>審查時經過編輯</span>" : ""}</div><pre class="attempt-output">${escapeHTML(attempt.content)}</pre></div>`);
    }
    return blocks.join("");
  }

  function roundLetter(calls, round) {
    const draft = draftOfRound(calls, round);
    if (!draft) return '<p class="letter-copy">這次產製早於逐輪紀錄，沒有留下這一輪的信件原文。</p>';
    return `<pre class="attempt-output">${escapeHTML(callText(draft))}</pre>`;
  }

  function draftOfRound(calls, round) {
    const drafts = calls.filter((call) => call.role === "drafter" && (call.round || 0) === round);
    return drafts.find((call) => call.ok) || drafts[drafts.length - 1];
  }

  function lastDraft(calls, round) {
    const draft = draftOfRound(calls, round);
    return draft ? callText(draft) : null;
  }

  function roundRunners(calls, round) {
    const runners = [];
    for (const call of calls) {
      if ((call.round || 0) !== round) continue;
      const label = `${CALL_ROLE[call.role] || call.role} ${call.runner}${call.model ? `・${call.model}` : ""}${call.ok ? "" : "（呼叫未通過）"}`;
      if (!runners.includes(label)) runners.push(label);
    }
    return runners.map((label) => `<span>${escapeHTML(label)}</span>`).join("");
  }

  // Each round's verdict comes from the review log, which holds exactly one line
  // per round. The reviewer's own call carries the same verdict as a list of
  // separate issues, so it is preferred where it exists — one issue per line
  // reads as the list of changes it is.
  function roundOutcome(line, calls, round) {
    if (!line) return "";
    if (line === "approve") return '<p class="attempt-verdict is-final">審核通過。</p>';
    if (line === "finalized") return '<p class="attempt-verdict is-final">輪數用完，直接定稿。</p>';
    if (line.startsWith("error: ")) return `<p class="attempt-verdict is-negative">呼叫失敗：${escapeHTML(line.slice(7))}</p>`;
    if (line.startsWith("guard: ")) return `<p class="attempt-verdict is-negative">未通過保護規則：${escapeHTML(line.slice(7))}</p>`;
    const issues = reviewIssues(calls, round);
    if (!issues.length) return `<p class="attempt-verdict">${escapeHTML(reviewLogLine(line))}</p>`;
    return `<p class="attempt-verdict">修改建議</p><ul class="attempt-issues">${issues.map((issue) => `<li>${escapeHTML(issue)}</li>`).join("")}</ul>`;
  }

  function reviewIssues(calls, round) {
    const review = calls.filter((call) => call.role === "reviewer" && (call.round || 0) === round && call.ok).pop();
    if (!review) return [];
    const parsed = parseJSONObject(review.output);
    if (!Array.isArray(parsed?.issues)) return [];
    return parsed.issues.map((issue) => (typeof issue === "string" ? issue : JSON.stringify(issue))).filter(Boolean);
  }

  // A drafter call answers with the letter wrapped in the contract's JSON, which
  // reads as an escaped one-line blob. The letter itself is what the round is
  // about, so it is unwrapped; whatever fails to parse is shown as it came.
  function callText(call) {
    const parsed = parseJSONObject(call.output);
    return typeof parsed?.letter === "string" && parsed.letter.trim() ? parsed.letter : call.output;
  }

  // A runner may wrap its JSON in prose or a code fence, so the object is taken
  // from the first brace to the last, the same leniency the server parses with.
  function parseJSONObject(output) {
    const start = (output || "").indexOf("{");
    const end = (output || "").lastIndexOf("}");
    if (start < 0 || end <= start) return null;
    try {
      return JSON.parse(output.slice(start, end + 1));
    } catch {
      return null;
    }
  }

  // The review log is written for the audit trail, so its prefixes are read back
  // into the words the rest of the interface uses.
  function reviewLogLine(line) {
    if (line.startsWith("revise: ")) return `要求修改：${line.slice(8)}`;
    if (line.startsWith("guard: ")) return `未通過保護規則：${line.slice(7)}`;
    if (line.startsWith("error: ")) return `呼叫失敗：${line.slice(7)}`;
    if (line === "approve") return "審查通過";
    if (line === "finalized") return "輪數用完，直接定稿";
    return line;
  }

  // A verdict the user disagrees with is redone as a whole: the screening
  // rejection is as likely to be the wrong part as the score. A job still being
  // processed has nothing to redo, and a letter history is the user's own.
  function canReprocess(job) {
    return ["filtered_out", "scored", "shortlisted"].includes(job.process_state);
  }

  function reprocessButton(job) {
    if (!canReprocess(job) || state.connected === false) return "";
    const busy = state.busy.has("reprocess");
    return `<button id="reprocess" class="button is-secondary" type="button" ${busy ? "disabled" : ""} aria-label="重新處理這筆職缺">${busy ? '<span class="spinner" aria-hidden="true"></span>' : icon("refresh")}重新處理</button>`;
  }

  // pendingCopy says why a waiting job is still waiting, because the three
  // reasons need different actions: nothing (it is already being consumed), the
  // switch, or the day's budget — and the last two are exactly what the push
  // ignores.
  function pendingCopy(job) {
    const stage = job.verdict === "pending_score" ? "評分" : "篩選";
    if (state.pushedJobIDs.has(job.id)) return `已排在最前面處理，完成後會自動更新。`;
    if (state.settings && state.settings.resident_worker === false) return `設定檔已停用常駐 worker，這筆等待手動批次消化。`;
    if (state.settings && state.settings.auto_processing === false) return `自動${stage}已關閉，這筆會留在原地，可用「馬上處理」單獨處理。`;
    if (state.context.budget_exhausted) return `今日${stage}額度已用盡，職缺會保留至隔日；「馬上處理」仍可單獨處理這一筆。`;
    return `${stage}正在背景處理，完成後會自動更新；「馬上處理」可讓這筆插到最前面。`;
  }

  // A job that is only waiting has nothing to redo, so its action is the push
  // rather than a reprocess: it goes to the head of the worker's queue and runs
  // whether automatic processing is off or the day's budget is spent. Once
  // pushed, the same slot reports the stage it is in.
  function processNowButton(job) {
    const label = job.verdict === "pending_score" ? "評分" : "篩選";
    if (state.pushedJobIDs.has(job.id) || state.busy.has("process-now")) {
      return `<button class="button is-primary" type="button" disabled><span class="spinner" aria-hidden="true"></span>正在${label}</button>`;
    }
    if (state.settings && state.settings.resident_worker === false) {
      return `<button class="button is-primary" type="button" disabled>等待手動批次</button>`;
    }
    return `<button id="process-now" class="button is-primary" type="button" aria-label="立即處理這筆職缺">${icon("spark")}馬上處理</button>`;
  }

  function actionDock(job) {
    const source = `<a id="source" class="button is-secondary" href="${escapeHTML(job.url || "#")}" target="_blank" rel="noreferrer" aria-label="開啟原始職缺">${icon("external")}</a>`;
    if (state.connected === false) return `<div class="action-dock">${source}<button class="button is-primary" type="button" disabled>${icon("cloud")}等待重新連線</button></div>`;
    if (job.letter_state === "ready" && (job.letter?.status === "approved" || job.letter?.status === "finalized")) return `<div class="action-dock">${source}<button id="request-letter" class="button is-secondary" type="button" ${state.busy.has("letter") ? "disabled" : ""} aria-label="重新產製這封求職信">${state.busy.has("letter") ? '<span class="spinner" aria-hidden="true"></span>' : icon("refresh")}重新產製</button><button id="copy" class="button is-primary" type="button">${icon("copy")}複製求職信</button></div>`;
    if (job.letter_state === "requested") return `<div class="action-dock">${source}<button id="request-letter" class="button is-primary" type="button" disabled><span class="spinner" aria-hidden="true"></span>求職信產生中</button></div>`;
    if (job.verdict === "recommended") return `<div class="action-dock">${source}${reprocessButton(job)}<button id="request-letter" class="button is-primary" type="button" ${state.busy.has("letter") ? "disabled" : ""}>${icon("spark")}${job.letter_state === "failed" ? "再次產生求職信" : "產生求職信"}</button></div>`;
    if (job.verdict === "pending_screen" || job.verdict === "pending_score") return `<div class="action-dock">${source}${processNowButton(job)}</div>`;
    return `<div class="action-dock">${source}${reprocessButton(job)}<button id="next-job" class="button is-primary" type="button">開下一筆${icon("arrow")}</button></div>`;
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
    const pending = job.verdict === "pending_screen" || job.verdict === "pending_score" ? `<div class="notice is-warning" role="status"><div class="notice-title"><span class="spinner" aria-hidden="true"></span>${job.verdict === "pending_score" ? "條件篩選已通過" : "等待條件篩選"}</div><p>${escapeHTML(pendingCopy(job))}</p></div>` : "";
    const stale = [
      job.filter_stale && job.filter_result_revision ? `篩選結論使用舊版硬性條件 ${shortRevision(job.filter_result_revision)}` : "",
      job.score_stale ? `評分使用舊版軟性偏好 ${shortRevision(job.score_result_revision)}` : "",
      job.letter_stale ? `求職信使用舊版 Profile ${shortRevision(job.letter_revision)}` : "",
    ].filter(Boolean);
    const staleNotice = stale.length ? `<div class="notice is-warning" role="status"><div class="notice-title">${icon("info")}待更新的舊版結果</div><p>${escapeHTML(stale.join("；"))}。可從系統頁手動更新過時評分；既有投遞與信件歷史不會被改寫。</p></div>` : "";
    const hits = filterCard(job);
    const total = job.score?.total ?? job.score_total;
    const reason = job.score?.reason || (job.verdict === "pending_score" ? "評分正在背景處理。" : job.filter_hits?.length ? "此職缺命中設定的排除條件。" : "尚無評分理由。");
    root.innerHTML = `
      <div class="section-stack">
        ${offline}${pending}${staleNotice}
        <div class="eyebrow-row"><span id="verdict-label" class="verdict-badge is-${verdict.tone}">${icon(verdict.icon)}${verdict.label}</span><span class="updated-at">${escapeHTML(job.source || "")}</span></div>
        <div class="job-hero"><div><h1 class="job-title">${escapeHTML(text(job.title))}</h1><p class="company-name">${escapeHTML(text(job.company_name))}</p>${revisionBadges(job)}</div>${total == null ? "" : `<div class="score-total"><strong>${escapeHTML(displayScore(total))}</strong><span>總分 / 100</span></div>`}</div>
        <p class="meta-line"><span class="meta-item">${icon("pin")}${escapeHTML(text(job.location))}</span><span class="meta-item">${icon("wallet")}${escapeHTML(salary(job))}</span></p>
        <section class="reason-card ${job.verdict === "unfit" ? "is-negative" : ""}"><strong>${job.verdict === "unfit" ? "排除理由" : job.verdict === "pending_score" ? "目前進度" : "判定理由"}</strong><p>${escapeHTML(reason)}</p></section>
        ${hits}${scoreCard(job)}${groupCard(job)}
        <details class="card details-card"><summary>職缺內容摘要</summary><div class="details-content"><p id="description">${escapeHTML(text(job.description))}</p></div></details>
        ${letterCard(job)}
      </div>
      ${actionDock(job)}`;
    bindCurrentActions();
  }

  const CONDITION_VERDICTS = { pass: "通過", fail: "未通過", unknown: "資訊不足" };

  // filterCard shows why a job was judged unfit, or which facts screening could
  // not decide — a list of condition names alone cannot answer either question.
  function filterCard(job) {
    const conditions = job.filter_result?.conditions || [];
    if (!conditions.length) {
      if (!job.filter_hits?.length) return "";
      return `<section class="card"><div class="card-heading"><h2>未通過的條件</h2><span>${job.filter_hits.length} 項</span></div><ul class="filter-hits">${job.filter_hits.map((hit) => `<li>${escapeHTML(hit)}</li>`).join("")}</ul></section>`;
    }
    const group = (kind) => conditions.filter((condition) => condition.kind === kind);
    const rows = (items) => items.map((condition) => `<li class="condition-row is-${escapeHTML(condition.verdict)}"><span class="condition-text">${escapeHTML(condition.text)}</span><span class="condition-verdict">${escapeHTML(CONDITION_VERDICTS[condition.verdict] || condition.verdict)}</span></li>`).join("");
    const required = group("required");
    const bonus = group("bonus");
    const bonusBlock = bonus.length ? `<h3 class="condition-subheading">加分條件（不影響適合與否）</h3><ul class="filter-hits">${rows(bonus)}</ul>` : "";
    return `<section class="card"><div class="card-heading"><h2>條件逐條結論</h2><span>${required.length} 項必備</span></div><ul class="filter-hits">${rows(required)}</ul>${bonusBlock}</section>`;
  }

  function renderQueue() {
    const root = document.querySelector("#screen-queue");
    // Every job here waits on the same action: open its page so the content
    // script can read the JD text the list page never carried.
    const rows = state.queue.map((job) => {
      const meta = `<span class="row-meta"><span>${escapeHTML(text(job.source))}</span><span>${escapeHTML(text(job.location))}</span><span>${escapeHTML(salary(job))}</span></span>`;
      const copy = `<span class="job-row-copy"><strong>${escapeHTML(text(job.title))}</strong><span>${escapeHTML(text(job.company_name))}</span>${meta}</span>`;
      return `<a class="job-row" href="${escapeHTML(job.url)}" target="_blank" rel="noreferrer">${copy}${icon("arrow", "row-arrow")}</a>`;
    }).join("");
    const loadMore = state.queueNextCursor ? `<button id="load-more-queue" class="load-more-button" type="button" ${state.busy.has("queue-page") ? "disabled" : ""}>${state.busy.has("queue-page") ? "載入中…" : "載入更多"}</button>` : "";
    root.innerHTML = `<div class="screen-heading"><div><h1>待看清單</h1><p>清單頁只帶得回摘要，點開原始職缺讓系統讀到 JD 全文後才會進入篩選。</p></div></div><div id="queue" class="job-list">${rows || emptyState("沒有待看職缺", "目前沒有需要補全文的職缺。")}</div>${loadMore}`;
    root.querySelector("#load-more-queue")?.addEventListener("click", loadMoreQueue);
  }

  function filterOptions() {
    return `<details class="card filter-panel" ${state.filtersOpen ? "open" : ""}><summary>進階篩選</summary><div class="filter-grid">
      <label>判定<select id="verdict"><option value="recommended">推薦</option><option value="not_recommended">不推薦</option><option value="pending_score">評分中</option><option value="pending_screen">篩選中</option><option value="pending_detail">待看</option><option value="unfit">不適合</option><option value="">全部</option></select></label>
      <label>處理狀態<select id="process"><option value="">全部</option><option value="discovered">待看</option><option value="new">篩選中</option><option value="queued">評分中</option><option value="scored">已評分</option><option value="shortlisted">已入選</option><option value="letter_requested">信件產生中</option><option value="letter_ready">信件就緒</option><option value="letter_failed">信件產製失敗</option><option value="filtered_out">已排除</option></select></label>
      <label>投遞狀態<select id="apply"><option value="">全部</option><option value="pending">待投遞</option><option value="applied">已投遞</option><option value="interview">面試</option><option value="offer">錄取</option><option value="ghosted">無回音</option><option value="dropped">放棄</option></select></label>
      <label>來源<select id="source-filter"><option value="">全部</option><option value="yourator">Yourator</option><option value="cake">Cake</option><option value="104">104</option></select></label>
    </div></details>`;
  }

  function renderShortlist() {
    const root = document.querySelector("#screen-shortlist");
    const rows = state.jobs.map((job) => {
      const viewed = state.viewedJobIDs.has(job.id);
      const viewedBadge = viewed ? `<span class="viewed-badge">${icon("check")}已看</span>` : "";
      return `<button class="job-row ${viewed ? "is-viewed" : ""}" type="button" data-job-id="${job.id}"><span class="row-score" aria-label="總分 ${displayScore(job.score_total)}">${displayScore(job.score_total)}</span><span class="job-row-copy"><strong>${escapeHTML(text(job.title))}</strong><span>${escapeHTML(text(job.company_name))}</span><span class="row-meta"><span>${escapeHTML(text(job.source))}</span><span>${escapeHTML(verdictMeta(job).label)}</span><span>${escapeHTML(text(job.apply_state))}</span>${viewedBadge}</span>${revisionBadges(job)}</span>${icon("arrow", "row-arrow")}</button>`;
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

  // A batch's facts are read at a glance or not at all, so every key is shown in
  // the same wording the rest of the UI uses for the same thing. An unmapped key
  // falls through as itself rather than being hidden: a fact nobody named yet is
  // still a fact.
  const RUN_TRIGGERS = { timer: "每日排程", "manual-cli": "手動（指令列）", "manual-extension": "手動（側邊欄）" };
  const RUN_STATS = { queries: "查詢數", fetched: "抓取", new: "新職缺", errors: "錯誤" };
  const RUN_STATES = {
    running: { label: "執行中", tone: "" },
    stalled: { label: "已中斷", tone: "is-warning" },
    done: { label: "已完成", tone: "" },
    failed: { label: "失敗", tone: "is-warning" },
  };
  const RUN_STATS_ORDER = ["queries", "fetched", "new", "errors"];

  function runStats(run) {
    const stats = Object.entries(run.stats || {})
      .sort(([a], [b]) => RUN_STATS_ORDER.indexOf(a) - RUN_STATS_ORDER.indexOf(b))
      .map(([key, value]) => `${RUN_STATS[key] || key} ${value}`);
    const verdicts = Object.entries(run.verdicts || {})
      .filter(([, value]) => value)
      .map(([key, value]) => `${VERDICTS[key]?.label || key} ${value}`);
    return [...stats, ...verdicts];
  }

  // runStartLabel drops the timezone suffix and the seconds: the batch list is
  // read for when and how long, not for a timestamp to correlate against.
  function runStartLabel(run) {
    return String(run.started_at || "").replace("T", " ").slice(0, 16);
  }

  function runStateMeta(run) {
    return RUN_STATES[run.state] || { label: "狀態未知", tone: "" };
  }

  // runElapsed reports how long a batch has been running, which is what tells a
  // slow fetch from a stopped one while it is still going.
  function runElapsed(run) {
    const startedAt = Date.parse(run.started_at);
    const endedAt = run.finished_at ? Date.parse(run.finished_at) : Date.now();
    if (!Number.isFinite(startedAt) || !Number.isFinite(endedAt)) return "";
    return duration(endedAt - startedAt);
  }

  // duration words a span the way a person reads a clock: seconds while it is
  // seconds, minutes after that. Anything longer than an hour is a fetch nobody
  // is watching in real time, so hours are enough precision.
  function duration(ms) {
    const seconds = Math.max(0, Math.round(ms / 1000));
    if (seconds < 60) return `${seconds} 秒`;
    const minutes = Math.floor(seconds / 60);
    if (minutes < 60) return `${minutes} 分 ${seconds % 60} 秒`;
    return `${Math.floor(minutes / 60)} 小時 ${minutes % 60} 分`;
  }

  const PROGRESS_STATES = [
    ["discovered", "待看"],
    ["new", "篩選中"],
    ["queued", "評分中"],
    ["letter_requested", "信件產生中"],
  ];

  const FAILURE_KINDS = {
    runner_error: "CLI 回報錯誤（額度、認證或逾時）",
    empty_output: "CLI 沒有輸出",
    no_json: "回應沒有 JSON 物件",
    invalid_json: "JSON 無法解析",
    reason_too_long: "理由超過 100 字上限",
    score_out_of_range: "分數超出 0–100",
    invalid_condition: "條件拆解不符合契約（列舉、分組或年資欄位）",
    invalid_content: "回應未通過內容驗證",
  };

  function formatTokens(n) {
    return Number(n || 0).toLocaleString("en-US");
  }

  function formatCost(usd) {
    const value = Number(usd || 0);
    return value > 0 ? `$${value.toFixed(4)}` : "—";
  }

  function agentCallRow(call) {
    const at = String(call.created_at || "").replace("T", " ").slice(0, 19);
    const seconds = Math.round(Number(call.duration_ms || 0) / 100) / 10;
    const kind = call.ok ? "" : FAILURE_KINDS[call.failure_kind] || "未分類的失敗";
    const detail = call.ok ? "" : `<span class="run-detail"><strong>${escapeHTML(kind)}</strong><br>${escapeHTML(call.detail || "未記錄回應內容")}</span>`;
    const totalTokens = Number(call.input_tokens || 0) + Number(call.output_tokens || 0);
    const cost = formatCost(call.cost_usd);
    const tokenLine = `${formatTokens(totalTokens)} tokens（入 ${formatTokens(call.input_tokens)}，出 ${formatTokens(call.output_tokens)}）${cost === "—" ? "" : ` ${cost}`}`;
    return `<article class="run-item ${call.ok ? "" : "is-failed"}"><div class="run-heading"><strong>${escapeHTML(call.role)} · ${escapeHTML(call.runner)}${call.model ? ` (${escapeHTML(call.model)})` : ""}</strong><span>${escapeHTML(at)}</span></div><div class="run-tokens">${escapeHTML(tokenLine)}</div><div class="run-stats"><span>${call.ok ? "呼叫成功" : "呼叫未完成"}</span><span>職缺 ${escapeHTML(String(call.job_id ?? "—"))}</span><span>${escapeHTML(String(seconds))}s</span></div>${detail}</article>`;
  }

  function dailyUsageSection() {
    const usage = state.progress?.agent_usage_daily || [];
    if (!usage.length) {
      return `<section aria-labelledby="usage-title"><div class="card-heading"><h2 id="usage-title">每日 Token 用量</h2><span class="usage-subtitle">台灣時間 00:00 重置<br>依 AI 分開統計</span></div>${emptyState("尚無用量紀錄", "Agent 呼叫後會依日期與 AI 顯示消耗量。")}</section>`;
    }
    const byDate = new Map();
    for (const row of usage) {
      if (!byDate.has(row.date)) byDate.set(row.date, []);
      byDate.get(row.date).push(row);
    }
    const days = [...byDate.entries()].map(([date, rows]) => {
      const dayTotal = rows.reduce((sum, row) => sum + Number(row.input_tokens || 0) + Number(row.output_tokens || 0), 0);
      const dayCost = rows.reduce((sum, row) => sum + Number(row.cost_usd || 0), 0);
      const lines = rows.map((row) => {
        const label = row.model ? `${row.runner} (${row.model})` : row.runner;
        const total = Number(row.input_tokens || 0) + Number(row.output_tokens || 0);
        const cost = Number(row.cost_usd) > 0 ? `<span>${escapeHTML(formatCost(row.cost_usd))}</span>` : "";
        return `<div class="system-row"><span class="system-copy"><strong>${escapeHTML(label)}</strong><span>${escapeHTML(String(row.calls))} 次呼叫 · 入 ${formatTokens(row.input_tokens)} · 出 ${formatTokens(row.output_tokens)}</span></span><span class="metric-value usage-metric"><span>${formatTokens(total)}</span>${cost}</span></div>`;
      }).join("");
      return `<div class="system-card"><div class="usage-day-heading"><strong>${escapeHTML(date)}</strong><span>共 ${formatTokens(dayTotal)} tokens${dayCost > 0 ? ` · ${escapeHTML(formatCost(dayCost))}` : ""}</span></div>${lines}</div>`;
    }).join("");
    return `<section class="card system-group" aria-labelledby="usage-title"><div class="card-heading"><h2 id="usage-title">每日 Token 用量</h2><span class="usage-subtitle">台灣時間 00:00 重置<br>依 AI 分開統計</span></div>${days}</section>`;
  }

  const STAGE_LABELS = { fetch: "抓取職缺", filter: "篩選", score: "評分", letter: "產生求職信" };

  // activitySection answers the one question every other panel leaves open:
  // whether anything is happening right now. Every other signal on this page is
  // written when a unit of work ends — a job's state moves, an Agent call is
  // audited — so a stage spending four minutes inside one call leaves them all
  // unchanged, and a stopped process looks exactly the same.
  function activitySection() {
    const units = state.progress?.in_flight || [];
    const fetching = state.runs.find((run) => run.state === "running");
    const rows = [];
    if (fetching) {
      const fetched = Number(fetching.stats?.fetched || 0);
      rows.push({ title: STAGE_LABELS.fetch, detail: `已收 ${fetched} 筆`, elapsed: runElapsed(fetching) });
    }
    for (const unit of units) {
      rows.push({
        title: STAGE_LABELS[unit.stage] || unit.stage,
        detail: unit.job_id ? `職缺 ${unit.job_id}` : "整批作業",
        elapsed: duration(Number(unit.elapsed_ms || 0)),
      });
    }
    if (!rows.length) {
      const copy = state.connected === false
        ? "連線恢復後會顯示正在執行的作業。"
        : "抓取、篩選、評分或求職信開始後，會在這裡顯示已經跑了多久。";
      return `<section aria-labelledby="activity-title"><div class="card-heading"><h2 id="activity-title">進行中</h2></div>${emptyState("目前沒有進行中的作業", copy)}</section>`;
    }
    const lines = rows.map((row) => `<div class="system-row"><span class="system-copy"><strong>${escapeHTML(row.title)}</strong><span>${escapeHTML(row.detail)}</span></span><span class="metric-value">${escapeHTML(row.elapsed)}</span></div>`).join("");
    return `<section class="card system-group" aria-labelledby="activity-title"><div class="card-heading"><h2 id="activity-title">進行中</h2><span>${rows.length} 項 · 每 5 秒更新</span></div><div class="system-card">${lines}</div></section>`;
  }

  function progressSection() {
    const progress = state.progress;
    if (!progress) return `<section aria-labelledby="progress-title"><div class="card-heading"><h2 id="progress-title">處理進度</h2></div>${emptyState("尚無處理進度", state.connected === false ? "連線恢復後會顯示待處理職缺與 Agent 呼叫。" : "重新整理後會顯示待處理職缺與 Agent 呼叫。")}</section>`;
    const counts = progress.jobs || {};
    const rows = PROGRESS_STATES.map(([key, label]) => `<div class="system-row"><span class="system-copy"><strong>${label}</strong><span>${escapeHTML(key)}</span></span><span class="metric-value">${Number(counts[key] || 0)}</span></div>`).join("");
    const budgetRow = (label, budget) => (budget?.limited
      ? `<div class="system-row"><span class="system-copy"><strong>${label}</strong><span>用盡後職缺保留至隔日</span></span><span class="metric-value ${Number(budget.remaining) === 0 ? "is-warning" : ""}">剩 ${Number(budget.remaining || 0)}</span></div>`
      : `<div class="system-row"><span class="system-copy"><strong>${label}</strong><span>未設定上限</span></span><span class="metric-value">不限</span></div>`);
    const budgets = budgetRow("今日篩選額度", progress.filter_budget) + budgetRow("今日評分額度", progress.score_budget);
    const calls = (progress.agent_calls || []).map(agentCallRow).join("");
    return `<section class="card system-group" aria-labelledby="progress-title"><div class="card-heading"><h2 id="progress-title">處理進度</h2><span>常駐 worker 待消化的職缺</span></div><div class="system-card">${rows}${budgets}</div></section>
      ${dailyUsageSection()}
      <section aria-labelledby="agent-calls-title"><div class="card-heading"><h2 id="agent-calls-title">Agent 呼叫紀錄</h2><span>最近 20 筆</span></div><div id="agent-calls" class="run-list">${calls || emptyState("尚無 Agent 呼叫", "評分或求職信執行後會顯示在這裡。")}</div></section>`;
  }

  // autoProcessingSection carries the token brake: with it off the worker stops
  // screening and scoring by itself, collection keeps running, and each job is
  // processed only when the user asks for it on that job.
  function autoProcessingSection() {
    const settings = state.settings;
    const enabled = settings ? settings.auto_processing !== false : true;
    const resident = settings ? settings.resident_worker !== false : true;
    const busy = state.busy.has("auto-processing");
    const copy = !resident
      ? "設定檔已停用常駐 worker，篩選與評分改由 CLI 批次驅動，此開關與「馬上處理」皆無作用。"
      : enabled
        ? "新職缺會自動完成篩選與評分。"
        : "已停止自動篩選與評分，職缺會停在待篩選／待評分，求職信與收集不受影響。";
    const disabled = state.connected === false || !settings || !resident || busy;
    return `<section class="card system-group" aria-labelledby="auto-title"><div class="card-heading"><h2 id="auto-title">自動篩選與評分</h2><span>${enabled && resident ? "開啟" : "關閉"}</span></div>
      <div class="system-card"><div class="system-row"><span class="system-copy"><strong>自動處理</strong><span>關閉後不再自動消耗 Agent 額度</span></span><span class="system-status ${enabled && resident ? "" : "is-warning"}"><span class="connection-dot"></span>${enabled && resident ? "運作中" : "已停止"}</span></div></div>
      <p class="reprocess-copy">${escapeHTML(copy)}</p>
      <button id="toggle-auto-processing" class="button ${enabled ? "is-secondary" : "is-primary"} is-full" type="button" ${disabled ? "disabled" : ""}>${busy ? '<span class="spinner" aria-hidden="true"></span>' : ""}${enabled ? "關閉自動篩選與評分" : "開啟自動篩選與評分"}</button></section>`;
  }

  // duplicatesSection presents each suspected pair side by side. It reports the
  // similarity and the reason so the user can see why the rules stopped short of
  // merging, and it offers exactly the two decisions they can make.
  function duplicatesSection() {
    const reasons = { title_similar: "職稱相似但不相同", location_mismatch: "職稱相同但地區不同", has_output: "已有評分或求職信，不自動合併" };
    if (!state.duplicates.length) {
      return `<section aria-labelledby="duplicates-title"><div class="card-heading"><h2 id="duplicates-title">疑似重複</h2><span>待裁決</span></div>${emptyState("沒有待裁決的疑似重複", "跨來源判定明確的重複職缺會自動合併。")}</section>`;
    }
    const items = state.duplicates.map((candidate) => {
      const side = (entry) => `<div class="dupe-side"><strong>${escapeHTML(text(entry.title))}</strong><span>${escapeHTML(text(entry.company_name))}</span><span class="row-meta"><span>${escapeHTML(SOURCE_LABELS[entry.source] || entry.source)}</span><span>${escapeHTML(text(entry.location))}</span></span><a href="${escapeHTML(entry.url)}" target="_blank" rel="noreferrer">開啟原始職缺</a></div>`;
      const busy = state.busy.has(`duplicate-${candidate.id}`);
      const percent = Math.round(Number(candidate.similarity || 0) * 100);
      return `<article class="run-item"><div class="run-heading"><strong>${escapeHTML(reasons[candidate.reason] || candidate.reason)}</strong><span>職稱相似度 ${percent}%</span></div>
        <div class="dupe-compare">${side(candidate.a)}${side(candidate.b)}</div>
        <div class="dupe-actions"><button class="button is-primary" type="button" data-merge="${Number(candidate.id)}" ${state.connected === false || busy ? "disabled" : ""}>合併為同一職缺</button><button class="button is-secondary" type="button" data-ignore="${Number(candidate.id)}" ${state.connected === false || busy ? "disabled" : ""}>忽略</button></div></article>`;
    }).join("");
    return `<section aria-labelledby="duplicates-title"><div class="card-heading"><h2 id="duplicates-title">疑似重複</h2><span>${state.duplicates.length} 組待裁決</span></div><div id="duplicates" class="run-list">${items}</div></section>`;
  }

  function renderSystem() {
    const root = document.querySelector("#screen-system");
    const runs = state.runs.map((run) => {
      const meta = runStateMeta(run);
      const stalled = run.state === "stalled" ? '<span class="run-detail">批次超過五分鐘沒有回報進度，執行它的程序可能已經結束。</span>' : "";
      return `<article class="run-item ${run.state === "failed" ? "is-failed" : ""}"><div class="run-heading"><strong>${escapeHTML(RUN_TRIGGERS[run.trigger] || text(run.trigger))}</strong><span>${escapeHTML(runStartLabel(run))}</span></div>
        <div class="run-stats"><span class="system-status ${meta.tone}"><span class="connection-dot"></span>${escapeHTML(meta.label)}</span><span>耗時 ${escapeHTML(runElapsed(run))}</span>${runStats(run).map((value) => `<span>${escapeHTML(value)}</span>`).join("")}${run.error ? `<span>${escapeHTML(run.error)}</span>` : ""}</div>${stalled}</article>`;
    }).join("");
    const profile = state.profile;
    const profileStatus = profile?.status || (state.connected === false ? "offline" : "loading");
    const summary = profile?.summary || {};
    const revisions = `篩選 ${shortRevision(profile?.filter_revision)} · 評分 ${shortRevision(profile?.score_revision)}`;
    const profileCopy = profileStatus === "ready"
      ? `年資 ${escapeHTML(text(summary.total_years))} 年（管理職 ${escapeHTML(text(summary.management_years))} 年） · ${escapeHTML(text(summary.skill_count))} 項技能 · ${escapeHTML(text(summary.experience_count))} 段經歷<br>${escapeHTML(summary.directions?.join?.("、") || "方向未提供")} · ${escapeHTML(revisions)}`
      : profileStatus === "invalid"
        ? `Profile 檔案需要人工修復。${profile.issues?.length ? `目前有 ${profile.issues.length} 項安全檢查問題。` : ""}`
        : profileStatus === "missing" ? "尚未建立履歷與求職條件。" : "目前無法讀取 Profile 狀態。";
    const profileAction = profileStatus === "missing" ? "開始設定" : profileStatus === "ready" ? "編輯履歷與求職條件" : profileStatus === "invalid" ? "查看修復說明" : "稍後再試";
    const runUnavailable = ["missing", "invalid", "degraded", "offline"].includes(profileStatus);
    const estimate = profile?.reprocess_estimate || {};
    const staleJobs = Number(estimate.partial_screened || 0) + Number(estimate.refiltered || 0) + Number(estimate.requeued || 0);
    const protectedJobs = Number(estimate.protected || 0);
    root.innerHTML = `<div class="section-stack"><div class="screen-heading"><div><h1>系統</h1><p>連線、Profile、批次與執行歷程。</p></div></div>
      ${activitySection()}
      <section class="card system-group" aria-labelledby="connection-title"><div class="card-heading"><h2 id="connection-title">連線與設定</h2></div><div class="system-card"><div class="system-row"><span class="system-copy"><strong>localhost API</strong><span>Side Panel 的 loopback 連線</span></span><span class="system-status ${state.connected ? "" : "is-warning"}"><span class="connection-dot"></span>${state.connected ? "正常" : "離線"}</span></div></div><button id="open-options" class="button is-secondary is-full" type="button">開啟連線設定</button></section>
      <section class="card profile-card" aria-labelledby="profile-title"><div class="card-heading"><h2 id="profile-title">Profile</h2><span class="profile-state is-${escapeHTML(profileStatus)}">${escapeHTML(profileStatus)}</span></div><p>${profileCopy}</p>${profileStatus === "ready" ? `<p class="reprocess-copy">${staleJobs ? `${staleJobs} 筆職缺使用舊版 Profile，等待手動更新。` : "所有可更新職缺均使用目前 Profile。"}${protectedJobs ? `另有 ${protectedJobs} 筆求職信歷史受保護。` : ""}</p>` : ""}<div class="button-stack"><button id="open-profile" class="button is-secondary is-full" type="button" ${profileStatus === "offline" || profileStatus === "loading" ? "disabled" : ""}>${escapeHTML(profileAction)}</button><button id="reprocess-profile" class="button is-primary is-full" type="button" ${profileStatus !== "ready" || staleJobs === 0 || state.busy.has("reprocess") ? "disabled" : ""}>${state.busy.has("reprocess") ? '<span class="spinner" aria-hidden="true"></span>正在排入更新' : `${icon("refresh")}更新過時判定職缺`}</button></div></section>
      ${autoProcessingSection()}
      ${duplicatesSection()}
      <section class="card system-group" aria-labelledby="batch-title"><div class="card-heading"><h2 id="batch-title">自動與手動批次</h2></div><div class="system-card"><div class="system-row"><span class="system-copy"><strong>自動抓取時間</strong><span>每日 08:30（Asia/Taipei）</span></span><span class="metric-value">每日</span></div></div><button id="run" class="button is-primary is-full" type="button" ${state.connected === false || state.busy.has("run") || runUnavailable ? "disabled" : ""}>${state.busy.has("run") ? '<span class="spinner" aria-hidden="true"></span>抓取已開始' : `${icon("play")}立即手動抓取`}</button></section>
      ${progressSection()}
      <section aria-labelledby="runs-title"><div class="card-heading"><h2 id="runs-title">批次歷程</h2><span>只記抓取事實</span></div><div id="runs" class="run-list">${runs || emptyState("尚無執行記錄", "手動抓取或排程執行後會顯示在這裡。")}</div></section></div>`;
    document.querySelector("#run").addEventListener("click", startRun);
    document.querySelector("#open-profile")?.addEventListener("click", () => chrome.tabs.create({ url: chrome.runtime.getURL("profile/index.html") }));
    document.querySelector("#open-options")?.addEventListener("click", () => chrome.runtime.openOptionsPage());
    document.querySelector("#reprocess-profile")?.addEventListener("click", reprocessProfile);
    document.querySelector("#toggle-auto-processing")?.addEventListener("click", toggleAutoProcessing);
    for (const button of document.querySelectorAll("[data-merge]")) {
      button.addEventListener("click", () => decideDuplicate(Number(button.dataset.merge), "merge"));
    }
    for (const button of document.querySelectorAll("[data-ignore]")) {
      button.addEventListener("click", () => decideDuplicate(Number(button.dataset.ignore), "ignore"));
    }
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
    const [jobs, queue, runs, progress, duplicates, profile] = await Promise.all([api(`/api/v1/jobs?${query}`), api("/api/v1/queue"), api("/api/v1/runs"), api("/api/v1/status"), api("/api/v1/duplicates"), chrome.runtime.sendMessage({ type: "profile-api", path: "/api/v1/profile", method: "GET" })]);
    if (request !== state.collectionRequest) return false;
    const connected = [jobs, queue, runs, progress, duplicates, profile].some((result) => result?.ok);
    setConnection(connected);
    if (jobs?.ok) {
      state.jobs = jobs.data.items || [];
      state.jobsNextCursor = jobs.data.next_cursor || null;
    }
    if (queue?.ok) {
      state.queue = queue.data.items || [];
      state.queueNextCursor = queue.data.next_cursor || null;
    }
    if (runs?.ok) state.runs = runs.data.items || [];
    if (duplicates?.ok) state.duplicates = duplicates.data.items || [];
    state.progress = progress?.ok ? progress.data : null;
    state.settings = progress?.ok ? progress.data.settings || null : null;
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

  async function loadMoreQueue() {
    if (!state.queueNextCursor || state.busy.has("queue-page")) return;
    const request = state.collectionRequest;
    const cursor = state.queueNextCursor;
    state.busy.add("queue-page");
    renderQueue();
    const params = new URLSearchParams();
    params.set("cursor", cursor);
    const result = await api(`/api/v1/queue?${params}`);
    state.busy.delete("queue-page");
    if (request !== state.collectionRequest || cursor !== state.queueNextCursor) {
      renderAll();
      return;
    }
    if (!result?.ok) {
      renderQueue();
      return showToast(result?.error || "無法載入更多待看職缺");
    }
    const known = new Set(state.queue.map((job) => job.id));
    for (const job of result.data.items || []) {
      if (!known.has(job.id)) state.queue.push(job);
    }
    state.queueNextCursor = result.data.next_cursor || null;
    renderAll();
  }

  function bindCurrentActions() {
    document.querySelector("#copy")?.addEventListener("click", copyLetter);
    document.querySelector("#request-letter")?.addEventListener("click", requestLetter);
    document.querySelector("#reprocess")?.addEventListener("click", reprocessJob);
    document.querySelector("#process-now")?.addEventListener("click", processJobNow);
    document.querySelector("#save-apply")?.addEventListener("click", saveApply);
    document.querySelector("[data-letter-history]")?.addEventListener("click", toggleLetterHistory);
    document.querySelector("#next-job")?.addEventListener("click", () => switchTab("queue"));
    for (const button of document.querySelectorAll("[data-unmerge]")) {
      button.addEventListener("click", () => unmergeJob(Number(button.dataset.unmerge)));
    }
  }

  // toggleLetterHistory owns the section's open state itself rather than letting
  // the details element keep it: the panel is re-rendered on every job update,
  // and a summary click that only toggled the DOM would close again on the next
  // render.
  async function toggleLetterHistory(event) {
    const jobID = Number(event.currentTarget.dataset.letterHistory);
    const history = state.letterHistory;
    if (history.open && history.jobID === jobID) {
      state.letterHistory = { jobID, attempts: history.attempts, open: false };
      renderAll();
      return;
    }
    event.preventDefault();
    // What was read a moment ago is not what the section is for: a generation
    // requested since then, or one still running, only shows up on a fresh read.
    // The cached copy is painted first so the section opens without a blank.
    const cached = history.jobID === jobID ? history.attempts : null;
    state.letterHistory = { jobID, attempts: cached, open: true };
    renderAll();
    const result = await api(`/api/v1/jobs/${jobID}/letter-history`);
    if (!result?.ok) {
      state.letterHistory = { jobID: null, attempts: null, open: false };
      renderAll();
      return showToast(result?.error || "無法讀取產製歷程");
    }
    state.letterHistory = { jobID, attempts: result.data?.attempts || [], open: true };
    renderAll();
  }

  // unmergeJob undoes one grouping decision. The alias returns to the state it
  // had before the merge; nothing that was already produced is deleted.
  async function unmergeJob(jobID) {
    if (state.busy.has("unmerge")) return;
    state.busy.add("unmerge");
    renderAll();
    const result = await api(`/api/v1/jobs/${jobID}/unmerge`, "POST");
    state.busy.delete("unmerge");
    if (!result?.ok) {
      renderAll();
      return showToast(result?.error || "無法取消合併");
    }
    // The current job keeps carrying the group, so it is re-read rather than
    // patched: after an unmerge it has one member fewer.
    if (state.currentJob) {
      const job = await api(`/api/v1/jobs/${state.currentJob.id}`);
      if (job?.ok) state.currentJob = job.data;
    }
    await loadCollections();
    renderAll();
    showToast("已取消合併");
  }

  // decideDuplicate applies the user's ruling on one suspected pair. Nothing is
  // ever decided automatically here: the program rules only merge what they are
  // certain of, and this is where the rest is settled.
  async function decideDuplicate(candidateID, decision) {
    const key = `duplicate-${candidateID}`;
    if (state.busy.has(key)) return;
    state.busy.add(key);
    renderAll();
    const result = await api(`/api/v1/duplicates/${candidateID}/${decision}`, "POST");
    state.busy.delete(key);
    if (!result?.ok) {
      renderAll();
      return showToast(result?.error || (decision === "merge" ? "無法合併" : "無法忽略"));
    }
    await loadCollections();
    renderAll();
    showToast(decision === "merge" ? "已合併為同一職缺" : "已忽略這組疑似重複");
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
    // The history now has a generation the cached copy does not know about, so
    // the section starts closed and reads afresh when it is next opened.
    state.letterHistory = { jobID: null, attempts: null, open: false };
    renderAll();
    showToast("已受理求職信生成要求");
  }

  // reprocessJob sends one job back through screening and scoring without
  // touching the rest, so a single wrong verdict costs one job's Agent calls
  // instead of a whole-database reprocess.
  async function reprocessJob() {
    if (!state.currentJob || state.busy.has("reprocess")) return;
    state.busy.add("reprocess");
    renderAll();
    const result = await api(`/api/v1/jobs/${state.currentJob.id}/reprocess`, "POST");
    state.busy.delete("reprocess");
    if (!result?.ok) {
      renderAll();
      return showToast(result?.error || "無法重新處理");
    }
    state.currentJob = result.data.job;
    await loadCollections();
    renderAll();
    startPollingIfNeeded();
    showToast("已排入重新處理");
  }

  // processJobNow asks the service to screen and score this one job ahead of the
  // worker's batch. The request only reports that it was accepted; the result
  // arrives through the same polling that shows any other processing job.
  async function processJobNow() {
    if (!state.currentJob || state.busy.has("process-now")) return;
    const jobID = state.currentJob.id;
    state.busy.add("process-now");
    renderAll();
    const result = await api(`/api/v1/jobs/${jobID}/process`, "POST");
    state.busy.delete("process-now");
    if (!result?.ok) {
      renderAll();
      return showToast(result?.error || "無法立即處理");
    }
    state.pushedJobIDs.add(jobID);
    state.currentJob = result.data.job;
    renderAll();
    startPollingIfNeeded();
    showToast("已插隊處理這筆職缺");
  }

  // toggleAutoProcessing flips the token brake. Collection and the user's own
  // single-job requests are unaffected either way.
  async function toggleAutoProcessing() {
    if (state.busy.has("auto-processing")) return;
    const enabled = state.settings ? state.settings.auto_processing !== false : true;
    state.busy.add("auto-processing");
    renderAll();
    const result = await api("/api/v1/settings", "PUT", { auto_processing: !enabled });
    state.busy.delete("auto-processing");
    if (!result?.ok) {
      renderAll();
      return showToast(result?.error || "無法更新自動處理設定");
    }
    state.settings = result.data;
    renderAll();
    showToast(enabled ? "已關閉自動篩選與評分" : "已開啟自動篩選與評分");
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
    const activation = result.data?.activation || {};
    const refiltered = Number(activation.refiltered || 0) + Number(activation.partial_screened || 0);
    const requeued = Number(activation.requeued || 0);
    const parts = [refiltered ? `${refiltered} 筆重新篩選` : "", requeued ? `${requeued} 筆重新評分` : ""].filter(Boolean);
    showToast(parts.length ? `已排入 ${parts.join("、")}` : "過時職缺已更新");
  }

  const ACTIVITY_POLL_MS = 5000;

  // The activity poll runs only while the system page is open and something is
  // actually running, so an idle panel makes no requests at all. It refreshes
  // just the two collections the running view reads, and it stops on its own the
  // first pass that finds nothing in flight.
  function activeWork() {
    return (state.progress?.in_flight || []).length > 0 || state.runs.some((run) => run.state === "running");
  }

  function scheduleActivityPoll() {
    clearTimeout(state.activityTimer);
    if (state.activeTab !== "system" || !activeWork()) return;
    state.activityTimer = setTimeout(pollActivity, ACTIVITY_POLL_MS);
  }

  async function pollActivity() {
    if (state.activeTab !== "system") return;
    const [progress, runs] = await Promise.all([api("/api/v1/status"), api("/api/v1/runs")]);
    if (progress?.ok) state.progress = progress.data;
    if (runs?.ok) state.runs = runs.data.items || [];
    if (progress?.ok || runs?.ok) {
      setConnection(true);
      renderSystem();
    }
    scheduleActivityPoll();
  }

  // Both pending verdicts are polled: a reprocessed job passes through screening
  // before it is scored, and a screening that ends in 不適合 is as much a result
  // the user is waiting for as a score.
  function processing(job) {
    return job?.verdict === "pending_screen" || job?.verdict === "pending_score";
  }

  function startPollingIfNeeded() {
    clearTimeout(state.pollTimer);
    if (!processing(state.currentJob)) return;
    // A pushed job is being worked on right now, so it is polled even when the
    // day's budget is spent or automatic processing is off — those hold back the
    // worker, not the user's own request.
    const pushed = state.pushedJobIDs.has(state.currentJob.id);
    if (!pushed && (state.context.budget_exhausted || state.settings?.auto_processing === false)) return;
    state.pollUntil = Date.now() + POLL_LIMIT_MS;
    state.pollTimer = setTimeout(pollCurrent, POLL_INTERVAL_MS);
  }

  async function pollCurrent() {
    if (!processing(state.currentJob)) return;
    if (Date.now() >= state.pollUntil) {
      // The push is no longer known to be running, so the job returns to being a
      // waiting one and can be pushed again.
      state.pushedJobIDs.delete(state.currentJob.id);
      renderAll();
      showToast("仍在處理，可稍後重新整理");
      return;
    }
    const result = await api(`/api/v1/jobs/${state.currentJob.id}`);
    if (result?.ok) {
      state.currentJob = result.data;
      renderAll();
      if (!processing(state.currentJob)) {
        state.pushedJobIDs.delete(state.currentJob.id);
        return;
      }
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
