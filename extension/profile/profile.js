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
    "requirements.locations": "requirement-locations",
    "requirements.industry_avoid": "industry-avoid",
    "requirements.exclude_title_keywords": "exclude-title",
    "requirements.exclude_description_keywords": "exclude-description",
    "requirements.exclude_companies": "exclude-companies",
    "intents.content_likes": "content-likes",
    "intents.content_dislikes": "content-dislikes",
    "intents.industry_interests": "industry-interests",
    honesty_bounds: "honesty-bounds",
  };
  const skillLevels = { expert: "專家", proficient: "熟練", familiar: "了解" };
  // locations mirrors the backend vocabulary: the stored value is the key, and a
  // locality's simplified, traditional and English wordings are matched from it.
  // Each key matches only the locality it names; `taiwan` is the country stated
  // without a county, and `overseas` is everything outside it.
  const locations = {
    taipei: "台北市", new_taipei: "新北市", keelung: "基隆市", taoyuan: "桃園市",
    hsinchu_city: "新竹市", hsinchu_county: "新竹縣", miaoli: "苗栗縣", taichung: "台中市",
    changhua: "彰化縣", nantou: "南投縣", yunlin: "雲林縣", chiayi_city: "嘉義市",
    chiayi_county: "嘉義縣", tainan: "台南市", kaohsiung: "高雄市", pingtung: "屏東縣",
    yilan: "宜蘭縣", hualien: "花蓮縣", taitung: "台東縣", penghu: "澎湖縣",
    kinmen: "金門縣", lienchiang: "連江縣", taiwan: "台灣", overseas: "海外",
  };
  // Every locality inside Taiwan, which is what the 全台 shortcut fills in: the
  // counties plus the country-only wording, never 海外.
  const taiwanLocations = Object.keys(locations).filter((key) => key !== "overseas");
  // vocabularyLists are the string lists whose entries are picked from a fixed
  // vocabulary instead of typed.
  const vocabularyLists = { "requirements.locations": locations };
  // employmentTypes mirrors the backend's controlled vocabulary: the stored
  // value is the key, and the label is only what this form shows.
  const employmentTypes = { full_time: "全職", part_time: "兼職", contract: "約聘", internship: "實習" };
  const educationStatuses = { graduated: "畢業", attended: "肄業" };
  const certificationStatuses = { active: "有效", expired: "過期", renewing: "過期重考中" };
  const languageLevels = { native: "母語", fluent: "流利", intermediate: "中等", basic: "基礎" };

  // selectOptions renders a controlled vocabulary. Every such field is picked,
  // never typed: a value outside the vocabulary is rejected on save.
  function selectOptions(terms, selected, placeholder = "請選擇") {
    return `<option value="">${placeholder}</option>` + Object.entries(terms).map(([value, label]) => `<option value="${value}" ${selected === value ? "selected" : ""}>${escapeHTML(label)}</option>`).join("");
  }

  function emptyProfile() {
    return {
      search: { directions: [] },
      requirements: {
        salary_min: 0, locations: [], remote: "", employment_types: [], industry_avoid: [],
        exclude_title_keywords: [], exclude_description_keywords: [], exclude_companies: [],
      },
      intents: { salary_target: 0, content_likes: [], content_dislikes: [], industry_interests: [] },
      experiences: [],
      qualifications: { education: [], skills: [], certifications: [], languages: [] },
      honesty_bounds: [],
    };
  }

  function emptyExperience() {
    return { industry: "", years: 0, is_management: false, exclude_from_totals: false, skills: [], org_type: "", role: "", achievements: [] };
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

  // Only the rows this list owns: an experience card contains its own nested
  // rows, which carry their own data-index and belong to a different array.
  function bindArrayControls(root, array, render) {
    root.querySelectorAll(":scope > [data-index]").forEach((row) => {
      const index = Number(row.dataset.index);
      row.querySelector('[data-action="delete"]')?.addEventListener("click", () => { array.splice(index, 1); render(); markDirty(); });
      row.querySelector('[data-action="up"]')?.addEventListener("click", () => { [array[index - 1], array[index]] = [array[index], array[index - 1]]; render(); markDirty(); });
      row.querySelector('[data-action="down"]')?.addEventListener("click", () => { [array[index + 1], array[index]] = [array[index], array[index + 1]]; render(); markDirty(); });
    });
  }

  function renderStringList(path) {
    const root = document.querySelector(`#${listTargets[path]}`);
    const array = getPath(path);
    const terms = vocabularyLists[path];
    root.innerHTML = array.map((value, index) => `<div class="compact-row" data-index="${index}">${terms ? `<select class="grow" data-path="${path}.${index}" aria-label="${escapeHTML(path)} 第 ${index + 1} 項" required>${selectOptions(remainingTerms(terms, array, value), value)}</select>` : `<input value="${escapeHTML(value)}" data-path="${path}.${index}" aria-label="${escapeHTML(path)} 第 ${index + 1} 項" required />`}<span class="row-actions">${controlButton("向上移動", "up", index === 0)}${controlButton("向下移動", "down", index === array.length - 1)}${controlButton("刪除", "delete", false, true)}</span></div>`).join("");
    root.querySelectorAll("input,select").forEach((field, index) => field.addEventListener(terms ? "change" : "input", () => {
      array[index] = field.value;
      markDirty();
      if (terms) renderStringList(path);
    }));
    bindArrayControls(root, array, () => renderStringList(path));
  }

  // remainingTerms hides the values other rows already hold, so the same地區 cannot
  // be picked twice; the row's own value stays so it renders as selected.
  function remainingTerms(terms, chosen, own) {
    const taken = new Set(chosen.filter((value) => value !== own));
    return Object.fromEntries(Object.entries(terms).filter(([key]) => !taken.has(key)));
  }

  // Employment type is a fixed vocabulary, so it is picked rather than typed —
  // a free-typed wording would silently match no JD.
  function renderEmploymentTypes() {
    const root = document.querySelector("#employment-types");
    const selected = new Set(state.draft.requirements.employment_types);
    root.innerHTML = Object.entries(employmentTypes).map(([value, label]) => `<label class="option-chip"><input type="checkbox" value="${value}" ${selected.has(value) ? "checked" : ""} /> ${escapeHTML(label)}</label>`).join("");
    root.querySelectorAll("input").forEach((input) => input.addEventListener("change", () => {
      state.draft.requirements.employment_types = Object.keys(employmentTypes).filter((value) => root.querySelector(`input[value="${value}"]`).checked);
      markDirty();
    }));
  }

  function nestedListHTML(values, path, label) {
    return values.map((value, index) => `<div class="compact-row" data-index="${index}"><input value="${escapeHTML(value)}" data-nested-path="${path}" data-nested-index="${index}" data-path="${path}.${index}" aria-label="${label} ${index + 1}" required /><span class="row-actions">${controlButton("向上移動", "up", index === 0)}${controlButton("向下移動", "down", index === values.length - 1)}${controlButton("刪除", "delete", false, true)}</span></div>`).join("");
  }

  function bindNestedList(root, values, render) {
    root.querySelectorAll("[data-nested-index]").forEach((input) => input.addEventListener("input", () => { values[Number(input.dataset.nestedIndex)] = input.value; markDirty(); }));
    bindArrayControls(root, values, render);
  }

  // derivedTotals mirrors the totals the backend materializes on save, so the
  // years a hard rule will compare against are visible while editing.
  function derivedTotals() {
    const totals = { total: 0, management: 0, industries: new Map() };
    for (const item of state.draft.experiences) {
      if (item.exclude_from_totals) continue;
      const years = Number(item.years) || 0;
      totals.total += years;
      if (item.is_management) totals.management += years;
      if (item.industry) totals.industries.set(item.industry, (totals.industries.get(item.industry) || 0) + years);
    }
    return totals;
  }

  function renderDerived() {
    const totals = derivedTotals();
    const industries = [...totals.industries.entries()].map(([industry, years]) => `${industry} ${years}`).join("、") || "—";
    document.querySelector("#derived-summary").textContent = `年資加總：總計 ${totals.total} 年、管理職 ${totals.management} 年；各產業 ${industries}（由下方經歷自動計算，不可手動編輯）`;
  }

  function renderExperiences() {
    const root = document.querySelector("#experiences");
    const array = state.draft.experiences;
    root.innerHTML = array.map((item, index) => `<article class="item-card" data-index="${index}"><div class="item-card-header"><strong>經歷 ${index + 1}</strong><span class="row-actions">${controlButton("向上移動經歷", "up", index === 0)}${controlButton("向下移動經歷", "down", index === array.length - 1)}${controlButton("刪除經歷", "delete", false, true)}</span></div><div class="field-grid"><label class="field">產業<input data-key="industry" data-path="experiences.${index}.industry" value="${escapeHTML(item.industry)}" required /></label><label class="field">年資<input data-key="years" data-path="experiences.${index}.years" type="number" min="0" step="0.1" value="${escapeHTML(item.years)}" required /></label></div><div class="field-grid"><label class="field is-checkbox"><input data-key="is_management" data-path="experiences.${index}.is_management" type="checkbox" ${item.is_management ? "checked" : ""} /> 這段是管理職</label><label class="field is-checkbox"><input data-key="exclude_from_totals" data-path="experiences.${index}.exclude_from_totals" type="checkbox" ${item.exclude_from_totals ? "checked" : ""} /> 不計入年資加總（實習或非相關經歷）</label></div><div class="field-grid"><label class="field">組織類型<input data-key="org_type" data-path="experiences.${index}.org_type" value="${escapeHTML(item.org_type)}" /></label><label class="field">角色<input data-key="role" data-path="experiences.${index}.role" value="${escapeHTML(item.role)}" /></label></div><div class="nested-list"><div class="subheading"><h3>量化成就</h3><button class="text-button" type="button" data-add-nested="achievements">＋ 新增</button></div><div class="compact-list" data-list="achievements">${nestedListHTML(item.achievements, `experiences.${index}.achievements`, "量化成就")}</div></div><div class="nested-list"><div class="subheading"><h3>使用技能</h3><button class="text-button" type="button" data-add-nested="skills">＋ 新增</button></div><div class="compact-list" data-list="skills">${nestedListHTML(item.skills, `experiences.${index}.skills`, "使用技能")}</div></div></article>`).join("");
    root.querySelectorAll(".item-card").forEach((card) => {
      const index = Number(card.dataset.index); const item = array[index];
      card.querySelectorAll("[data-key]").forEach((input) => input.addEventListener("input", () => {
        if (input.type === "checkbox") item[input.dataset.key] = input.checked;
        else item[input.dataset.key] = input.type === "number" ? Number(input.value) : input.value;
        renderDerived(); markDirty();
      }));
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
    bindArrayControls(root, array, () => { renderExperiences(); renderDerived(); });
    renderDerived();
  }

  // syncSkillsFromExperiences carries every skill named on an experience into the
  // totals list at the highest proficiency, for the user to lower where it does
  // not hold. It runs only when asked: a skill already on the list keeps both its
  // place and its proficiency, so loading again never overwrites an adjustment.
  function syncSkillsFromExperiences() {
    const skills = state.draft.qualifications.skills;
    const known = new Set(skills.map((skill) => skill.name.trim().toLowerCase()));
    let added = false;
    for (const experience of state.draft.experiences) {
      for (const name of experience.skills) {
        const key = name.trim().toLowerCase();
        if (!key || known.has(key)) continue;
        known.add(key);
        skills.push({ name, level: "expert", from_experience: true });
        added = true;
      }
    }
    if (added) { renderSkills(); markDirty(); }
    return added;
  }

  // The three qualification lists are one item per row: what matters when reading
  // them back is whether anything is missing from the set, which a stack of cards
  // hides. Each row is a name plus its one graded field.
  function renderSkills() {
    const root = document.querySelector("#skills"); const array = state.draft.qualifications.skills;
    root.innerHTML = array.map((item, index) => gradedRow({
      index, total: array.length, path: `qualifications.skills.${index}`, label: "技能",
      name: item.name, namePlaceholder: "技能名稱", field: "level", value: item.level,
      terms: skillLevels, placeholder: "請選擇熟練度", note: item.from_experience ? "來自經歷" : "",
    })).join("");
    bindRowFields(root, array, renderSkills);
  }

  // gradedRow is one compact row of a qualification list: a name that grows and a
  // fixed-width vocabulary field beside it. The note slot is always laid out,
  // even empty, so every row of every list keeps the same column edges.
  function gradedRow({ index, total, path, label, name, namePlaceholder, field, value, terms, placeholder, note = "" }) {
    return `<div class="compact-row" data-index="${index}"><input class="grow" data-key="name" data-path="${path}.name" value="${escapeHTML(name)}" placeholder="${escapeHTML(namePlaceholder)}" aria-label="${label} ${index + 1} 名稱" required /><select class="fixed" data-key="${field}" data-path="${path}.${field}" aria-label="${label} ${index + 1}" required>${selectOptions(terms, value, placeholder)}</select><span class="row-note">${escapeHTML(note)}</span><span class="row-actions">${controlButton("向上移動", "up", index === 0)}${controlButton("向下移動", "down", index === total - 1)}${controlButton("刪除", "delete", false, true)}</span></div>`;
  }

  function bindRowFields(root, array, render) {
    root.querySelectorAll(":scope > [data-index]").forEach((row) => {
      const item = array[Number(row.dataset.index)];
      row.querySelectorAll("[data-key]").forEach((field) => field.addEventListener(field.tagName === "SELECT" ? "change" : "input", () => { item[field.dataset.key] = field.value; markDirty(); }));
    });
    bindArrayControls(root, array, render);
  }

  function renderEducation() {
    const root = document.querySelector("#education"); const array = state.draft.qualifications.education;
    const levels = { bachelor: "學士", master: "碩士", phd: "博士" };
    root.innerHTML = array.map((item, index) => `<div class="item-card" data-index="${index}"><div class="item-card-header"><strong>學歷 ${index + 1}</strong><span class="row-actions">${controlButton("向上移動", "up", index === 0)}${controlButton("向下移動", "down", index === array.length - 1)}${controlButton("刪除", "delete", false, true)}</span></div><div class="field-grid"><label class="field">學位<select data-key="level" data-path="qualifications.education.${index}.level" required>${selectOptions(levels, item.level)}</select></label><label class="field">科系領域<input data-key="field" data-path="qualifications.education.${index}.field" value="${escapeHTML(item.field)}" required /></label><label class="field">狀態<select data-key="status" data-path="qualifications.education.${index}.status" required>${selectOptions(educationStatuses, item.status)}</select></label></div></div>`).join("");
    bindItemFields(root, array, renderEducation);
  }

  function renderCertifications() {
    const root = document.querySelector("#certifications"); const array = state.draft.qualifications.certifications;
    root.innerHTML = array.map((item, index) => gradedRow({
      index, total: array.length, path: `qualifications.certifications.${index}`, label: "證照",
      name: item.name, namePlaceholder: "證照名稱", field: "status", value: item.status,
      terms: certificationStatuses, placeholder: "請選擇狀態",
    })).join("");
    bindRowFields(root, array, renderCertifications);
  }

  function renderLanguages() {
    const root = document.querySelector("#languages"); const array = state.draft.qualifications.languages;
    root.innerHTML = array.map((item, index) => gradedRow({
      index, total: array.length, path: `qualifications.languages.${index}`, label: "語言",
      name: item.name, namePlaceholder: "語言", field: "level", value: item.level,
      terms: languageLevels, placeholder: "請選擇程度",
    })).join("");
    bindRowFields(root, array, renderLanguages);
  }

  function bindItemFields(root, array, render) {
    root.querySelectorAll(".item-card").forEach((card) => {
      const item = array[Number(card.dataset.index)];
      card.querySelectorAll("[data-key]").forEach((input) => input.addEventListener("input", () => { item[input.dataset.key] = input.value; markDirty(); }));
    });
    bindArrayControls(root, array, render);
  }

  function renderDirections() {
    const root = document.querySelector("#directions"); const array = state.draft.search.directions;
    root.innerHTML = array.map((item, index) => `<article class="item-card" data-index="${index}"><div class="item-card-header"><strong>方向 ${index + 1}</strong><span class="row-actions">${controlButton("向上移動", "up", index === 0)}${controlButton("向下移動", "down", index === array.length - 1)}${controlButton("刪除", "delete", false, true)}</span></div><div class="field-grid"><label class="field">代碼<input data-key="key" data-path="search.directions.${index}.key" value="${escapeHTML(item.key)}" placeholder="P1" required /></label><label class="field">名稱<input data-key="title" data-path="search.directions.${index}.title" value="${escapeHTML(item.title)}" required /></label></div><div class="nested-list"><div class="subheading"><h3>搜尋關鍵字</h3><button class="text-button" type="button" data-add-keyword>＋ 新增</button></div><div class="compact-list" data-keywords>${nestedListHTML(item.keywords, `search.directions.${index}.keywords`, "搜尋關鍵字")}</div></div></article>`).join("");
    root.querySelectorAll(".item-card").forEach((card) => { const index = Number(card.dataset.index); const item = array[index]; card.querySelectorAll("[data-key]").forEach((input) => input.addEventListener("input", () => { item[input.dataset.key] = input.value; markDirty(); })); const list = card.querySelector("[data-keywords]"); bindNestedList(list, item.keywords, renderDirections); card.querySelector("[data-add-keyword]").addEventListener("click", () => { const itemIndex = item.keywords.length; item.keywords.push(""); renderDirections(); markDirty(); focusPath(`search.directions.${index}.keywords.${itemIndex}`); }); });
    bindArrayControls(root, array, renderDirections);
  }

  function renderDynamic() {
    Object.keys(listTargets).forEach(renderStringList);
    renderEmploymentTypes();
    renderDirections(); renderExperiences(); renderEducation(); renderSkills(); renderCertifications(); renderLanguages();
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
    // 全台 is not a value: it is every locality in Taiwan at once, so the button
    // fills the list rather than adding a row. Entries already chosen stay put,
    // and 海外 is left alone — it is the one locality the shortcut never means.
    document.querySelectorAll("[data-fill-taiwan]").forEach((button) => button.addEventListener("click", () => {
      const path = button.dataset.fillTaiwan;
      const chosen = getPath(path);
      const added = taiwanLocations.filter((key) => !chosen.includes(key));
      if (added.length === 0) return;
      const kept = chosen.filter((value) => value !== "");
      chosen.splice(0, chosen.length, ...kept, ...added);
      renderStringList(path);
      markDirty();
    }));
    const adders = {
      experience: { array: () => state.draft.experiences, item: emptyExperience, render: renderExperiences, focus: (index) => `experiences.${index}.industry` },
      direction: { array: () => state.draft.search.directions, item: () => ({ key: "", title: "", keywords: [] }), render: renderDirections, focus: (index) => `search.directions.${index}.key` },
      education: { array: () => state.draft.qualifications.education, item: () => ({ level: "", field: "", status: "" }), render: renderEducation, focus: (index) => `qualifications.education.${index}.field` },
      skill: { array: () => state.draft.qualifications.skills, item: () => ({ name: "", level: "" }), render: renderSkills, focus: (index) => `qualifications.skills.${index}.name` },
      certification: { array: () => state.draft.qualifications.certifications, item: () => ({ name: "", status: "" }), render: renderCertifications, focus: (index) => `qualifications.certifications.${index}.name` },
      language: { array: () => state.draft.qualifications.languages, item: () => ({ name: "", level: "" }), render: renderLanguages, focus: (index) => `qualifications.languages.${index}.name` },
    };
    document.querySelector("[data-load-experience-skills]").addEventListener("click", () => {
      if (!syncSkillsFromExperiences()) showMessage("經歷裡的技能都已在技能總表中。", "success");
    });
    for (const [key, adder] of Object.entries(adders)) {
      document.querySelector(`[data-add="${key}"]`).addEventListener("click", () => {
        const array = adder.array(); const index = array.length;
        array.push(adder.item()); adder.render(); markDirty(); focusPath(adder.focus(index));
      });
    }
  }

  function normalizeProfile(profile) {
    const base = emptyProfile(); const input = profile || {};
    return {
      search: { ...base.search, ...(input.search || {}) },
      requirements: { ...base.requirements, ...(input.requirements || {}) },
      intents: { ...base.intents, ...(input.intents || {}) },
      experiences: (input.experiences || []).map((item) => ({ ...emptyExperience(), ...item })),
      qualifications: { ...base.qualifications, ...(input.qualifications || {}) },
      honesty_bounds: input.honesty_bounds || [],
    };
  }

  // payload strips the editor's own annotations and `derived`: the totals are
  // materialized by the backend, and sending them would be rejected.
  function payload() {
    const value = clone(state.draft);
    value.qualifications.skills = value.qualifications.skills.map(({ name, level }) => ({ name, level }));
    delete value.derived;
    return value;
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
    const draft = state.draft;
    if (!draft.search.directions.length) localIssues.push({ path: "search.directions", message: "至少需要一個求職方向" });
    draft.search.directions.forEach((direction, index) => {
      if (!direction.keywords.length) localIssues.push({ path: `search.directions.${index}.keywords`, message: "方向至少需要一個關鍵字" });
    });
    draft.requirements.locations.forEach((value, index) => {
      if (!locations[value]) localIssues.push({ path: `requirements.locations.${index}`, message: "請選擇地區" });
    });
    if (!["required", "preferred", "acceptable", "rejected"].includes(draft.requirements.remote)) localIssues.push({ path: "requirements.remote", message: "請選擇遠端意願" });
    if (!draft.experiences.length) localIssues.push({ path: "experiences", message: "至少需要一段工作經歷" });
    draft.experiences.forEach((experience, index) => {
      if (!experience.industry.trim()) localIssues.push({ path: `experiences.${index}.industry`, message: "經歷需要填寫產業" });
    });
    if (!draft.qualifications.skills.length) localIssues.push({ path: "qualifications.skills", message: "至少需要一項技能" });
    const seen = new Map();
    draft.qualifications.skills.forEach((skill, index) => {
      const key = skill.name.trim().toLowerCase();
      if (key && seen.has(key)) localIssues.push({ path: `qualifications.skills.${index}.name`, message: `技能「${skill.name}」重複` });
      else if (key) seen.set(key, index);
    });
    draft.qualifications.skills.forEach((skill, index) => {
      if (!skillLevels[skill.level]) localIssues.push({ path: `qualifications.skills.${index}.level`, message: "請選擇熟練度" });
    });
    draft.qualifications.education.forEach((entry, index) => {
      if (!educationStatuses[entry.status]) localIssues.push({ path: `qualifications.education.${index}.status`, message: "請選擇學歷狀態" });
    });
    draft.qualifications.certifications.forEach((entry, index) => {
      if (!certificationStatuses[entry.status]) localIssues.push({ path: `qualifications.certifications.${index}.status`, message: "請選擇證照狀態" });
    });
    draft.qualifications.languages.forEach((entry, index) => {
      if (!languageLevels[entry.level]) localIssues.push({ path: `qualifications.languages.${index}.level`, message: "請選擇語言程度" });
    });
    if (!draft.honesty_bounds.length) localIssues.push({ path: "honesty_bounds", message: "至少需要一項誠實邊界" });
    if (localIssues.length) { showMessage("請先完成必要欄位，再確認儲存。"); renderIssues(localIssues); return false; }
    return true;
  }

  function revisionLabel(data) {
    if (!data?.filter_revision && !data?.score_revision) return "尚未建立 Profile";
    return `篩選 ${shortRevision(data.filter_revision)} · 評分 ${shortRevision(data.score_revision)}`;
  }

  async function loadProfile() {
    clearFeedback(); loading.hidden = false; form.hidden = true; setVisualState("載入中");
    const result = await api("GET");
    if (!result?.ok) { loading.hidden = true; setVisualState("無法連線", "error"); showMessage(result?.error || "無法載入 Profile。請確認 API 服務與 Options 設定。"); return; }
    state.etag = result.etag || '"missing"'; state.status = result.data.status;
    revision.textContent = revisionLabel(result.data);
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

  // savedCopy describes which gates a save actually invalidated, because that is
  // what decides how much work reprocessing would cost.
  function savedCopy(data) {
    if (data.filter_changed) return "硬性條件已變更：更新後既有職缺需重新篩選，通過者再重新評分。";
    if (data.score_changed) return "只有軟性偏好變更：既有職缺的篩選結論保留，只需重新評分。";
    return "本次沒有影響判定的欄位變更，不需要重新處理任何職缺。";
  }

  async function saveProfile() {
    if (state.saving) return;
    clearFeedback(); state.saving = true; saveButton.disabled = true; setVisualState("儲存中");
    const result = await api("PUT", payload());
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
    revision.textContent = revisionLabel(result.data);
    setVisualState("已儲存", "ready"); document.querySelector("#dirty-copy").textContent = "所有內容已儲存並切換至目前 Profile。";
    showMessage(`Profile 已儲存（${revisionLabel(result.data)}）。${savedCopy(result.data)}既有職缺保留原判定，可在系統頁手動更新。`, "success");
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
