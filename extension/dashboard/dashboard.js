const status = document.querySelector("#status");
const verdictFilter = document.querySelector("#verdict"), process = document.querySelector("#process"), apply = document.querySelector("#apply"), sourceFilter = document.querySelector("#source-filter"), detail = document.querySelector("#detail"), source = document.querySelector("#source"), verdictLabel = document.querySelector("#verdict-label"), score = document.querySelector("#score"), description = document.querySelector("#description"), letter = document.querySelector("#letter"), letterState = document.querySelector("#letter-state"), requestLetter = document.querySelector("#request-letter"), applyState = document.querySelector("#apply-state"), events = document.querySelector("#events"), run = document.querySelector("#run"), refresh = document.querySelector("#refresh"), copy = document.querySelector("#copy"), saveApply = document.querySelector("#save-apply");
// The verdict wording comes from the API; the dashboard never derives a
// decision from process_state or a score of its own.
const VERDICTS = { unfit: "不適合", recommended: "推薦", not_recommended: "不推薦", pending_detail: "待補全文", pending_score: "待評分" };
let selectedJob = null;
function api(path, method = "GET", body) { return chrome.runtime.sendMessage({ type: "api", path, method, body }); }
function text(value) { return value == null || value === "" ? "—" : String(value); }
function message(value) { status.textContent = value; }
function node(tag, value) { const el = document.createElement(tag); el.textContent = value; return el; }
function verdictText(job) { return VERDICTS[job.verdict] || "—"; }
function renderJobs(items) { const root = document.querySelector("#jobs"); root.replaceChildren(...items.map((job) => { const button = node("button", `${verdictText(job)}｜${text(job.title)}｜${text(job.company_name)}｜分數 ${text(job.score_total)}｜${text(job.salary_min)}-${text(job.salary_max)}｜${text(job.location)}｜${text(job.process_state)}｜${text(job.apply_state)}`); button.addEventListener("click", () => showJob(job.id)); return button; })); }
function renderSimple(id, items, format, link) { const root = document.querySelector(id); root.replaceChildren(...items.map((item) => { const el = link ? document.createElement("a") : document.createElement("p"); el.textContent = format(item); if (link) { el.href = item.url; el.target = "_blank"; el.rel = "noreferrer"; } return el; })); }
function pairs(value) { return Object.entries(value || {}).sort(([a], [b]) => a.localeCompare(b)).map(([key, count]) => `${key}=${count}`).join(", "); }
function runLine(r) { return `${text(r.started_at)}｜${text(r.finished_at)}｜${text(r.trigger)}｜抓取 ${pairs(r.stats)}｜判定 ${pairs(r.verdicts)}｜${text(r.error)}`; }
async function load() { const params = new URLSearchParams(); if (verdictFilter.value) params.set("verdict", verdictFilter.value); if (process.value) params.set("process_state", process.value); if (apply.value) params.set("apply_state", apply.value); if (sourceFilter.value) params.set("source", sourceFilter.value); const [jobs, queue, runs] = await Promise.all([api(`/api/v1/jobs?${params}`), api("/api/v1/queue"), api("/api/v1/runs")]); for (const result of [jobs, queue, runs]) if (!result.ok) message(result.error); if (jobs.ok) { renderJobs(jobs.data.items); message("已連線至 jobfinder API"); } if (queue.ok) renderSimple("#queue", queue.data.items, (j) => `${text(j.title)}｜${text(j.company_name)}｜${text(j.salary_min)}-${text(j.salary_max)}`, true); if (runs.ok) renderSimple("#runs", runs.data.items, runLine); }
// The letter section follows letter_state: the system drafts nothing until the
// user asks, and the request is accepted rather than awaited.
function renderLetter(job) {
  const state = job.letter_state;
  letter.textContent = job.letter?.status === "approved" ? job.letter.content : "";
  copy.hidden = job.letter?.status !== "approved";
  requestLetter.hidden = !(state === "none" || state === "failed");
  requestLetter.disabled = false;
  requestLetter.textContent = state === "failed" ? "再次產生求職信" : "產生求職信";
  if (state === "none") letterState.textContent = "尚未要求產生求職信。";
  else if (state === "requested") letterState.textContent = "產生中，將於下一輪執行完成；重新整理即可取得結果。";
  else if (state === "ready") letterState.textContent = "求職信已過審。";
  else if (state === "failed") letterState.textContent = "求職信未過審。";
  else letterState.textContent = "求職信僅對推薦職缺開放。";
}
async function showJob(id) { const result = await api(`/api/v1/jobs/${id}`); if (!result.ok) return message(result.error); selectedJob = result.data; detail.hidden = false; source.href = selectedJob.url; document.querySelector("#detail-title").textContent = `${text(selectedJob.title)}｜${text(selectedJob.company_name)}`; verdictLabel.textContent = `判定：${verdictText(selectedJob)}${selectedJob.filter_hits ? `（命中 ${selectedJob.filter_hits.join("、")}）` : ""}`; score.textContent = selectedJob.score ? `總分 ${text(selectedJob.score.total)}｜技能 ${text(selectedJob.score.hard_skill)}｜領域 ${text(selectedJob.score.domain)}｜資歷 ${text(selectedJob.score.seniority)}｜條件 ${text(selectedJob.score.condition)}｜方向 ${text(selectedJob.score.direction)}：${text(selectedJob.score.reason)}` : "分數：—"; description.textContent = text(selectedJob.description); renderLetter(selectedJob); applyState.value = selectedJob.apply_state || "pending"; events.replaceChildren(...(selectedJob.status_events || []).map((e) => node("p", `${e.axis}: ${e.from_state || "—"} → ${e.to_state}`))); }
run.addEventListener("click", async () => { const result = await api("/api/v1/runs", "POST"); message(result.ok && result.data.status === "started" ? "已開始抓取" : result.ok ? "已有執行中的抓取" : result.error); await load(); });
refresh.addEventListener("click", load); verdictFilter.addEventListener("change", load); process.addEventListener("change", load); apply.addEventListener("change", load); sourceFilter.addEventListener("change", load);
copy.addEventListener("click", async () => { try { await navigator.clipboard.writeText(letter.textContent); message("已複製信件"); } catch (_) { message("無法使用剪貼簿，信件文字仍可選取複製。"); } });
saveApply.addEventListener("click", async () => { if (!selectedJob) return; const result = await api(`/api/v1/jobs/${selectedJob.id}/apply`, "POST", { apply_state: applyState.value }); if (!result.ok) return message(result.error); message("投遞狀態已更新"); await showJob(selectedJob.id); load(); });
requestLetter.addEventListener("click", async () => { if (!selectedJob) return; requestLetter.disabled = true; const result = await api(`/api/v1/jobs/${selectedJob.id}/letter`, "POST"); if (!result.ok) { requestLetter.disabled = false; return message(result.error); } message("已受理求職信生成要求"); selectedJob = result.data.job; renderLetter(selectedJob); load(); });
load();
