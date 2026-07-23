(() => {
  const form = document.querySelector("#profile-form");
  const loading = document.querySelector("#loading");
  const message = document.querySelector("#message");
  const issues = document.querySelector("#issues");
  const saveState = document.querySelector("#save-state");
  const revision = document.querySelector("#revision");
  const saveButton = document.querySelector("#save");
  const dialog = document.querySelector("#confirm-dialog");
  const themeToggle = document.querySelector("#theme-toggle");

  const state = { draft: null, etag: null, dirty: false, saving: false, status: "loading", theme: "light" };
  const listTargets = {
    "skills.expert": "skills-expert",
    "skills.proficient": "skills-proficient",
    "skills.familiar": "skills-familiar",
    "preferences.locations": "locations",
    "preferences.industry_avoid": "industry-avoid",
    "preferences.screening.exclude_title_keywords": "exclude-title",
    "preferences.screening.exclude_description_keywords": "exclude-description",
    "preferences.screening.require_any_keywords": "require-any",
    "preferences.screening.exclude_companies": "exclude-companies",
    honesty_bounds: "honesty-bounds",
  };

  function emptyProfile() {
    return {
      summary: "", years_of_experience: 0, education: { degree: "", field: "" }, experiences: [],
      skills: { expert: [], proficient: [], familiar: [] }, certifications: [],
      preferences: {
        salary_min: 0, salary_target: 0, locations: [], remote: "", directions: [], industry_avoid: [],
        screening: { exclude_title_keywords: [], exclude_description_keywords: [], require_any_keywords: [], exclude_companies: [] },
      }, honesty_bounds: [],
    };
  }

  function clone(value) { return JSON.parse(JSON.stringify(value)); }
  function escapeHTML(value) { return String(value ?? "").replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;").replaceAll('"', "&quot;").replaceAll("'", "&#039;"); }
  function api(method, body) { return chrome.runtime.sendMessage({ type: "profile-api", path: "/api/v1/profile", method, body, etag: state.etag }); }
  function shortRevision(value) { return value ? value.replace(/^sha256:/, "").slice(0, 8) : "—"; }
  function getPath(path) { return path.split(".").reduce((value, key) => value?.[key], state.draft); }
  function setPath(path, value) {
    const keys = path.split(".");
    const last = keys.pop();
    const parent = keys.reduce((object, key) => object[key], state.draft);
    parent[last] = value;
  }

  function setVisualState(label, kind = "") {
    saveState.textContent = label;
    saveState.className = `state-pill${kind ? ` is-${kind}` : ""}`;
  }

  function markDirty() {
    if (state.saving) return;
    state.dirty = true;
    setVisualState("尚未儲存", "dirty");
    document.querySelector("#dirty-copy").textContent = "草稿只保留在此頁面；離開前請確認是否儲存。";
  }

  function showMessage(text, kind = "error") {
    message.textContent = text;
    message.className = `message is-${kind}`;
    message.hidden = false;
    message.focus();
  }

  function clearFeedback() {
    message.hidden = true;
    issues.hidden = true;
    form.querySelectorAll('[data-field-error="true"]').forEach((node) => node.removeAttribute("data-field-error"));
  }

  function controlButton(label, action, disabled = false, destructive = false) {
    return `<button class="mini-button${destructive ? " is-delete" : ""}" type="button" data-action="${action}" aria-label="${label}" ${disabled ? "disabled" : ""}>${action === "up" ? "↑" : action === "down" ? "↓" : "×"}</button>`;
  }

  function bindArrayControls(root, array, render) {
    root.querySelectorAll("[data-index]").forEach((row) => {
      const index = Number(row.dataset.index);
      row.querySelector('[data-action="delete"]')?.addEventListener("click", () => { array.splice(index, 1); render(); markDirty(); });
      row.querySelector('[data-action="up"]')?.addEventListener("click", () => { [array[index - 1], array[index]] = [array[index], array[index - 1]]; render(); markDirty(); });
      row.querySelector('[data-action="down"]')?.addEventListener("click", () => { [array[index + 1], array[index]] = [array[index], array[index + 1]]; render(); markDirty(); });
    });
  }

  function renderStringList(path) {
    const root = document.querySelector(`#${listTargets[path]}`);
    const array = getPath(path);
    root.innerHTML = array.map((value, index) => `<div class="compact-row" data-index="${index}"><input value="${escapeHTML(value)}" data-path="${path}.${index}" aria-label="${escapeHTML(path)} 第 ${index + 1} 項" required /><span class="row-actions">${controlButton("向上移動", "up", index === 0)}${controlButton("向下移動", "down", index === array.length - 1)}${controlButton("刪除", "delete", false, true)}</span></div>`).join("");
    root.querySelectorAll("input").forEach((input, index) => input.addEventListener("input", () => { array[index] = input.value; markDirty(); }));
    bindArrayControls(root, array, () => renderStringList(path));
  }

  function nestedListHTML(values, path, label) {
    return values.map((value, index) => `<div class="compact-row" data-index="${index}"><input value="${escapeHTML(value)}" data-nested-path="${path}" data-nested-index="${index}" data-path="${path}.${index}" aria-label="${label} ${index + 1}" required /><span class="row-actions">${controlButton("向上移動", "up", index === 0)}${controlButton("向下移動", "down", index === values.length - 1)}${controlButton("刪除", "delete", false, true)}</span></div>`).join("");
  }

  function bindNestedList(root, values, render) {
    root.querySelectorAll("[data-nested-index]").forEach((input) => input.addEventListener("input", () => { values[Number(input.dataset.nestedIndex)] = input.value; markDirty(); }));
    bindArrayControls(root, values, render);
  }

  function renderExperiences() {
    const root = document.querySelector("#experiences");
    const array = state.draft.experiences;
    root.innerHTML = array.map((item, index) => `<article class="item-card" data-index="${index}"><div class="item-card-header"><strong>經歷 ${index + 1}</strong><span class="row-actions">${controlButton("向上移動經歷", "up", index === 0)}${controlButton("向下移動經歷", "down", index === array.length - 1)}${controlButton("刪除經歷", "delete", false, true)}</span></div><div class="field-grid"><label class="field">角色<input data-key="role" data-path="experiences.${index}.role" value="${escapeHTML(item.role)}" required /></label><label class="field">組織類型<input data-key="org_type" data-path="experiences.${index}.org_type" value="${escapeHTML(item.org_type)}" required /></label><label class="field">年資<input data-key="years" data-path="experiences.${index}.years" type="number" min="0" step="0.1" value="${escapeHTML(item.years)}" required /></label></div><label class="field">職責摘要<textarea data-key="summary" data-path="experiences.${index}.summary" rows="3" required>${escapeHTML(item.summary)}</textarea></label><div class="nested-list"><div class="subheading"><h3>量化成就</h3><button class="text-button" type="button" data-add-nested="achievements">＋ 新增</button></div><div class="compact-list" data-list="achievements">${nestedListHTML(item.achievements, `experiences.${index}.achievements`, "量化成就")}</div></div><div class="nested-list"><div class="subheading"><h3>使用技能</h3><button class="text-button" type="button" data-add-nested="skills">＋ 新增</button></div><div class="compact-list" data-list="skills">${nestedListHTML(item.skills, `experiences.${index}.skills`, "使用技能")}</div></div></article>`).join("");
    root.querySelectorAll(".item-card").forEach((card) => {
      const index = Number(card.dataset.index); const item = array[index];
      card.querySelectorAll("[data-key]").forEach((input) => input.addEventListener("input", () => { item[input.dataset.key] = input.type === "number" ? Number(input.value) : input.value; markDirty(); }));
      for (const key of ["achievements", "skills"]) {
        const list = card.querySelector(`[data-list="${key}"]`);
        bindNestedList(list, item[key], renderExperiences);
        card.querySelector(`[data-add-nested="${key}"]`).addEventListener("click", () => {
          const itemIndex = item[key].length;
          item[key].push(""); renderExperiences(); markDirty();
          focusPath(`experiences.${index}.${key}.${itemIndex}`);
        });
      }
    });
    bindArrayControls(root, array, renderExperiences);
  }

  function renderCertifications() {
    const root = document.querySelector("#certifications"); const array = state.draft.certifications;
    root.innerHTML = array.map((item, index) => `<div class="item-card" data-index="${index}"><div class="item-card-header"><strong>證照 ${index + 1}</strong><span class="row-actions">${controlButton("向上移動", "up", index === 0)}${controlButton("向下移動", "down", index === array.length - 1)}${controlButton("刪除", "delete", false, true)}</span></div><div class="field-grid"><label class="field">名稱<input data-key="name" data-path="certifications.${index}.name" value="${escapeHTML(item.name)}" required /></label><label class="field">狀態<input data-key="status" data-path="certifications.${index}.status" value="${escapeHTML(item.status)}" required /></label></div></div>`).join("");
    root.querySelectorAll(".item-card").forEach((card) => { const item = array[Number(card.dataset.index)]; card.querySelectorAll("[data-key]").forEach((input) => input.addEventListener("input", () => { item[input.dataset.key] = input.value; markDirty(); })); });
    bindArrayControls(root, array, renderCertifications);
  }

  function renderDirections() {
    const root = document.querySelector("#directions"); const array = state.draft.preferences.directions;
    root.innerHTML = array.map((item, index) => `<article class="item-card" data-index="${index}"><div class="item-card-header"><strong>方向 ${index + 1}</strong><span class="row-actions">${controlButton("向上移動", "up", index === 0)}${controlButton("向下移動", "down", index === array.length - 1)}${controlButton("刪除", "delete", false, true)}</span></div><div class="field-grid"><label class="field">代碼<input data-key="key" data-path="preferences.directions.${index}.key" value="${escapeHTML(item.key)}" placeholder="P1" required /></label><label class="field">名稱<input data-key="title" data-path="preferences.directions.${index}.title" value="${escapeHTML(item.title)}" required /></label></div><div class="nested-list"><div class="subheading"><h3>方向關鍵字</h3><button class="text-button" type="button" data-add-keyword>＋ 新增</button></div><div class="compact-list" data-keywords>${nestedListHTML(item.keywords, `preferences.directions.${index}.keywords`, "方向關鍵字")}</div></div></article>`).join("");
    root.querySelectorAll(".item-card").forEach((card) => { const index = Number(card.dataset.index); const item = array[index]; card.querySelectorAll("[data-key]").forEach((input) => input.addEventListener("input", () => { item[input.dataset.key] = input.value; markDirty(); })); const list = card.querySelector("[data-keywords]"); bindNestedList(list, item.keywords, renderDirections); card.querySelector("[data-add-keyword]").addEventListener("click", () => { const itemIndex = item.keywords.length; item.keywords.push(""); renderDirections(); markDirty(); focusPath(`preferences.directions.${index}.keywords.${itemIndex}`); }); });
    bindArrayControls(root, array, renderDirections);
  }

  function renderDynamic() {
    Object.keys(listTargets).forEach(renderStringList);
    renderExperiences(); renderCertifications(); renderDirections();
  }

  function fillStatic() {
    form.querySelectorAll("[data-path]").forEach((input) => {
      if (/\.\d+(\.|$)/.test(input.dataset.path)) return;
      const value = getPath(input.dataset.path);
      input.value = value ?? "";
      input.addEventListener("input", () => { setPath(input.dataset.path, input.type === "number" ? Number(input.value) : input.value); markDirty(); });
    });
  }

  function focusPath(path) {
    requestAnimationFrame(() => {
      const target = form.querySelector(`[data-path="${CSS.escape(path)}"]`);
      if (!target) return;
      target.focus({ preventScroll: true });
      target.scrollIntoView({ block: "nearest", inline: "nearest" });
    });
  }

  function addHandlers() {
    document.querySelectorAll("[data-add-list]").forEach((button) => button.addEventListener("click", () => { const path = button.dataset.addList; const index = getPath(path).length; getPath(path).push(""); renderStringList(path); markDirty(); focusPath(`${path}.${index}`); }));
    document.querySelector('[data-add="experience"]').addEventListener("click", () => { const index = state.draft.experiences.length; state.draft.experiences.push({ role: "", org_type: "", years: 0, summary: "", achievements: [], skills: [] }); renderExperiences(); markDirty(); focusPath(`experiences.${index}.role`); });
    document.querySelector('[data-add="certification"]').addEventListener("click", () => { const index = state.draft.certifications.length; state.draft.certifications.push({ name: "", status: "" }); renderCertifications(); markDirty(); focusPath(`certifications.${index}.name`); });
    document.querySelector('[data-add="direction"]').addEventListener("click", () => { const index = state.draft.preferences.directions.length; state.draft.preferences.directions.push({ key: "", title: "", keywords: [] }); renderDirections(); markDirty(); focusPath(`preferences.directions.${index}.key`); });
  }

  function normalizeProfile(profile) {
    const base = emptyProfile(); const input = profile || {};
    return {
      ...base, ...input,
      education: { ...base.education, ...(input.education || {}) },
      experiences: (input.experiences || []).map((item) => ({ role: "", org_type: "", years: 0, summary: "", achievements: [], skills: [], ...item })),
      skills: { ...base.skills, ...(input.skills || {}) }, certifications: input.certifications || [],
      preferences: { ...base.preferences, ...(input.preferences || {}), screening: { ...base.preferences.screening, ...(input.preferences?.screening || {}) } },
      honesty_bounds: input.honesty_bounds || [],
    };
  }

  function renderForm(profile) {
    state.draft = normalizeProfile(clone(profile));
    fillStatic(); renderDynamic(); addHandlers();
    loading.hidden = true; form.hidden = false;
  }

  function renderIssues(rawIssues) {
    const list = Array.isArray(rawIssues) ? rawIssues : [];
    form.querySelectorAll('[data-field-error="true"]').forEach((node) => node.removeAttribute("data-field-error"));
    const normalized = list.map((issue) => ({ path: issue.path || issue.field || "", message: issue.message || issue.code || "欄位內容不符合規則" }));
    issues.innerHTML = `<h2>請修正下列問題</h2><ul>${normalized.map((issue, index) => `<li><button type="button" data-issue-index="${index}">${escapeHTML(issue.path ? `${issue.path}：${issue.message}` : issue.message)}</button></li>`).join("") || "<li>Profile 內容未通過後端檢查。</li>"}</ul>`;
    issues.hidden = false;
    normalized.forEach((issue) => findField(issue.path)?.setAttribute("data-field-error", "true"));
    issues.querySelectorAll("[data-issue-index]").forEach((button) => button.addEventListener("click", () => findField(normalized[Number(button.dataset.issueIndex)].path)?.focus()));
    issues.focus();
  }

  function findField(path) {
    if (!path) return null;
    const normalized = path.replaceAll("[", ".").replaceAll("]", "").replace(/^profile\./, "");
    return [...form.querySelectorAll("[data-path]")].find((node) => node.dataset.path === normalized || normalized.startsWith(`${node.dataset.path}.`));
  }

  function validateDraft() {
    const localIssues = [];
    if (!state.draft.experiences.length) localIssues.push({ path: "experiences", message: "至少需要一段工作經歷" });
    if (!state.draft.preferences.locations.length) localIssues.push({ path: "preferences.locations", message: "至少需要一個可接受地點" });
    const allSkills = [...state.draft.skills.expert, ...state.draft.skills.proficient, ...state.draft.skills.familiar];
    if (!allSkills.length) localIssues.push({ path: "skills", message: "至少需要一項技能" });
    if (!state.draft.preferences.directions.length) localIssues.push({ path: "preferences.directions", message: "至少需要一個求職方向" });
    state.draft.preferences.directions.forEach((direction, index) => {
      if (!direction.keywords.length) localIssues.push({ path: `preferences.directions.${index}.keywords`, message: "方向至少需要一個關鍵字" });
    });
    if (!state.draft.honesty_bounds.length) localIssues.push({ path: "honesty_bounds", message: "至少需要一項誠實邊界" });
    const skillPaths = ["skills.expert", "skills.proficient", "skills.familiar"];
    const seen = new Map();
    skillPaths.forEach((path) => getPath(path).forEach((skill, index) => {
      const key = skill.trim().toLowerCase();
      if (key && seen.has(key)) localIssues.push({ path: `${path}.${index}`, message: `技能「${skill}」不可跨分級重複` });
      else if (key) seen.set(key, path);
    }));
    if (localIssues.length) { showMessage("請先完成必要欄位，再確認儲存。"); renderIssues(localIssues); return false; }
    return true;
  }

  async function loadProfile() {
    clearFeedback(); loading.hidden = false; form.hidden = true; setVisualState("載入中");
    const result = await api("GET");
    if (!result?.ok) { loading.hidden = true; setVisualState("無法連線", "error"); showMessage(result?.error || "無法載入 Profile。請確認 API 服務與 Options 設定。"); return; }
    state.etag = result.etag || '"missing"'; state.status = result.data.status;
    revision.textContent = result.data.profile_revision ? `revision ${shortRevision(result.data.profile_revision)}` : "尚未建立 Profile";
    if (!["missing", "ready"].includes(result.data.status)) {
      loading.hidden = true; setVisualState("需要人工修復", "error");
      showMessage("後端 Profile 檔案無法安全載入。請先在本機修復 YAML，再重新載入；編輯器不會強制覆蓋現有檔案。");
      if (result.data.issues?.length) renderIssues(result.data.issues);
      return;
    }
    renderForm(result.data.profile || emptyProfile()); state.dirty = false; setVisualState(result.data.status === "missing" ? "首次設定" : "已載入", "ready");
  }

  function openConfirmation() {
    dialog.showModal();
  }

  async function saveProfile() {
    if (state.saving) return;
    clearFeedback(); state.saving = true; saveButton.disabled = true; setVisualState("儲存中");
    const result = await api("PUT", state.draft);
    state.saving = false; saveButton.disabled = false;
    if (!result?.ok) {
      setVisualState("尚未儲存", "error");
      if (result?.status === 412 || result?.code === "profile_conflict") {
        showMessage("Profile 已由其他方式修改。你的草稿仍保留在本頁；請先自行複製需要的內容，再按重新載入取得最新版。");
        const reload = document.createElement("button"); reload.type = "button"; reload.className = "secondary-button"; reload.textContent = "重新載入"; reload.addEventListener("click", () => { if (confirm("重新載入會捨棄目前草稿，確定繼續？")) location.reload(); }); message.append(" ", reload);
      } else if (result?.status === 422 || result?.code === "profile_invalid") {
        showMessage("Profile 尚未通過安全與結構檢查，內容未寫入。", "error"); renderIssues(result.issues);
      } else showMessage(result?.error || "儲存失敗。草稿仍保留在此頁。", "error");
      return;
    }
    state.etag = result.etag || state.etag; state.dirty = false; state.status = "ready";
    revision.textContent = `revision ${shortRevision(result.data.profile_revision)}`;
    setVisualState("已儲存", "ready"); document.querySelector("#dirty-copy").textContent = "所有內容已儲存並切換至目前 Profile。";
    showMessage(`Profile 已儲存（revision ${shortRevision(result.data.profile_revision)}）。既有職缺保留原評分，可在系統頁手動更新。`, "success");
  }

  form.addEventListener("submit", (event) => {
    event.preventDefault(); clearFeedback();
    if (!form.checkValidity()) { form.reportValidity(); form.querySelector(":invalid")?.focus(); return; }
    if (!validateDraft()) return;
    openConfirmation();
  });
  document.querySelector("#confirm-save").addEventListener("click", (event) => { event.preventDefault(); dialog.close(); saveProfile(); });
  window.addEventListener("beforeunload", (event) => { if (!state.dirty && !state.saving) return; event.preventDefault(); event.returnValue = ""; });

  themeToggle.addEventListener("click", () => {
    state.theme = state.theme === "dark" ? "light" : "dark"; document.documentElement.dataset.theme = state.theme;
    themeToggle.setAttribute("aria-label", state.theme === "dark" ? "切換至淺色模式" : "切換至深色模式");
    chrome.storage.local.set({ theme: state.theme });
  });
  chrome.storage.local.get({ theme: "light" }, ({ theme }) => { state.theme = theme === "dark" ? "dark" : "light"; document.documentElement.dataset.theme = state.theme; themeToggle.setAttribute("aria-label", state.theme === "dark" ? "切換至淺色模式" : "切換至深色模式"); loadProfile(); });
})();
