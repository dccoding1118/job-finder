import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs";

const [mode, file, phase = "base"] = process.argv.slice(2);
if (!mode || !file) throw new Error("usage: assert-positive.mjs <schema|source|snapshot|live-snapshot|api|api-filter|api-detail|capture-list|capture-job> <file> [phase]");

const sha256 = (value) => crypto.createHash("sha256").update(value).digest("hex");
const readJSON = () => JSON.parse(fs.readFileSync(file, "utf8"));

if (mode === "schema") {
  const data = readJSON();
  assert.equal(data.schema_version, 2);
  assert.equal(data.journal_mode.toLowerCase(), "wal");
  assert.equal(data.foreign_keys, true);
  assert.deepEqual(data.tables, ["agent_calls", "jobs", "letters", "runs", "scores", "status_events"]);
  assert.deepEqual(data.jobs, []);
  process.exit(0);
}

if (mode === "agent-total") {
  const data = readJSON();
  process.stdout.write(String((data.agent_calls || []).reduce((sum, call) => sum + call.count, 0)));
  process.exit(0);
}

if (mode === "source") {
  const requests = fs.readFileSync(file, "utf8").trim().split("\n").filter(Boolean).map(JSON.parse);
  assert.equal(requests.length, 8);
  assert.equal(requests[0].path, "/robots.txt");
  const lists = requests.filter(({ path }) => path === "/api/v4/jobs");
  assert.equal(lists.length, 3);
  assert.deepEqual(lists.map(({ query }) => query["term[]"]), [["cloud", "platform"], ["backend", "Go"], ["Kubernetes", "reliability"]]);
  for (const list of lists) {
    assert.deepEqual({ method: list.method, page: list.query.page }, { method: "GET", page: ["1"] });
    assert.match(list.user_agent, /^jobfinder\/1\.0/);
    assert.equal(list.referer, "");
  }
  assert.deepEqual(requests.filter(({ path }) => path.startsWith("/jobs/")).map(({ method, path }) => ({ method, path })), [1000, 1001, 1002, 1003].map((id) => ({ method: "GET", path: `/jobs/${id}` })));
  process.exit(0);
}

if (mode === "live-snapshot") {
  const data = readJSON();
  assert.ok(data.jobs.length > 0, "live source returned zero jobs");
  const ids = new Set();
  for (const job of data.jobs) {
    assert.equal(job.source, "yourator");
    assert.ok(String(job.external_id).trim());
    assert.ok(!ids.has(job.external_id), `duplicate external_id ${job.external_id}`);
    ids.add(job.external_id);
    const url = new URL(job.url);
    assert.equal(url.protocol, "https:");
    assert.equal(url.hostname, "www.yourator.co");
    assert.ok(job.title.trim() && job.company_name.trim() && job.location.trim());
    assert.ok(job.description_length > 0);
    assert.match(job.description_sha256, /^[a-f0-9]{64}$/);
    assert.match(job.content_hash, /^[a-f0-9]{64}$/);
    assert.ok(["remote", "hybrid", "onsite"].includes(job.remote_type));
    assert.equal(job.salary_min === null, job.salary_max === null);
    if (job.salary_min !== null) assert.ok(job.salary_min >= 0 && job.salary_min <= job.salary_max);
  }
  const scored = data.jobs.filter(({ score }) => score !== null);
  const lettered = data.jobs.filter(({ letter }) => letter !== null);
  if (phase === "complete") {
    assert.equal(scored.length, 1);
    assert.equal(lettered.length, 1);
    assert.ok(["approved", "failed"].includes(lettered[0].letter.status));
    assert.ok(data.agent_calls.some(({ role, ok }) => role === "scorer" && ok));
    assert.ok(data.agent_calls.some(({ role, ok }) => role === "drafter" && ok));
    // A draft that fails the anti-hallucination guard never reaches the reviewer,
    // so "failed" is a legal terminal state without a reviewer call; only an
    // approved letter proves the reviewer ran.
    if (lettered[0].letter.status === "approved") {
      assert.ok(data.agent_calls.some(({ role, ok }) => role === "reviewer" && ok));
    }
  }
  process.stdout.write(JSON.stringify({ jobs: data.jobs.length, ids_sha256: sha256([...ids].sort().join("\n")), scored: scored.length, lettered: lettered.length }));
  process.exit(0);
}

// The four synthetic fixtures. process_state and letter change with the phase;
// every other field is fixed by the production adapter and fake Agent, so it is
// asserted identically in every phase.
const FIXTURES = {
  1000: { title: "Verification intern platform engineer", company: "Example Learning", description: "Training cloud platform work", salary: [60000, 70000], remote: "onsite" },
  1001: { title: "Verification failure remote platform engineer", company: "Example Platform", description: "Build Go & cloud platform services", salary: [100000, 120000], remote: "remote" },
  1002: { title: "Verification ready hybrid backend engineer", company: "Example Services", description: "Build Go backend services", salary: [110000, 130000], remote: "hybrid" },
  1003: { title: "Verification low score cloud engineer", company: "Example Operations", description: "Maintain cloud operations services", salary: [90000, 100000], remote: "onsite" },
};

// letterConsumed is true once the user has requested the two letters and the
// letter stage has been driven; base is the pre-request state.
function processStateOf(id, letterConsumed) {
  if (id === "1000") return "filtered_out";
  if (id === "1003") return "scored";
  if (!letterConsumed) return "shortlisted";
  return id === "1002" ? "letter_ready" : "letter_failed";
}

function transitionsOf(id, letterConsumed) {
  if (id === "1000") return ["process:->new", "process:new->filtered_out"];
  if (id === "1003") return ["process:->new", "process:new->queued", "process:queued->scored"];
  const base = ["process:->new", "process:new->queued", "process:queued->shortlisted"];
  if (!letterConsumed) return base;
  if (id === "1002") return [...base, "process:shortlisted->letter_requested", "process:letter_requested->letter_ready", "apply:->pending"];
  return [...base, "process:shortlisted->letter_requested", "process:letter_requested->letter_failed"];
}

if (mode === "snapshot") {
  const data = readJSON();
  const letterConsumed = phase === "lettered" || phase === "repeat";
  assert.equal(data.schema_version, 2);
  assert.equal(data.journal_mode.toLowerCase(), "wal");
  assert.equal(data.foreign_keys, true);
  assert.deepEqual(data.tables, ["agent_calls", "jobs", "letters", "runs", "scores", "status_events"]);
  assert.equal(data.jobs.length, 4);
  const jobs = Object.fromEntries(data.jobs.map((job) => [job.external_id, job]));

  for (const [id, expected] of Object.entries(FIXTURES)) {
    const job = jobs[id];
    assert.ok(job, `missing fixture ${id}`);
    assert.equal(job.source, "yourator");
    assert.equal(job.url, `http://127.0.0.1:18787/jobs/${id}`);
    assert.equal(job.title, expected.title);
    assert.equal(job.company_name, expected.company);
    assert.equal(job.company_info, "public listing");
    assert.equal(job.description_sha256, sha256(expected.description));
    assert.equal(job.description_length, Buffer.byteLength(expected.description));
    assert.deepEqual([job.salary_min, job.salary_max], expected.salary);
    assert.equal(job.location, "Taipei");
    assert.equal(job.remote_type, expected.remote);
    assert.equal(job.process_state, processStateOf(id, letterConsumed));
    const content = [expected.title, expected.description, ...expected.salary.map(String), "Taipei", expected.remote].join("\n");
    assert.equal(job.content_hash, sha256(content));
    assert.deepEqual(job.events.map(({ axis, from_state, to_state }) => `${axis}:${from_state}->${to_state}`), transitionsOf(id, letterConsumed));
  }

  // Filter is deterministic and LLM-free: one title is excluded, the rest are scored.
  assert.deepEqual(jobs[1000].filter_hits, ["exclude_title_keywords"]);
  assert.equal(jobs[1000].score, null);
  assert.equal(jobs[1000].letter, null);
  assert.equal(jobs[1003].score.total, 60);
  assert.deepEqual([jobs[1003].score.hard_skill, jobs[1003].score.domain, jobs[1003].score.seniority, jobs[1003].score.condition, jobs[1003].score.direction], [60, 60, 60, 60, 60]);
  assert.equal(jobs[1003].score.runner, "claude");
  assert.equal(jobs[1003].score.reason_sha256, sha256("合成低分情境"));
  assert.equal(jobs[1003].letter, null);
  assert.equal(jobs[1001].score.total, 80);
  assert.equal(jobs[1001].score.reason_sha256, sha256("合成重試情境"));
  assert.equal(jobs[1002].score.total, 90);
  assert.deepEqual([jobs[1002].score.hard_skill, jobs[1002].score.domain, jobs[1002].score.seniority, jobs[1002].score.condition, jobs[1002].score.direction], [90, 90, 90, 90, 90]);
  assert.equal(jobs[1002].score.reason_sha256, sha256("合成核准情境"));

  if (!letterConsumed) {
    // No letter is drafted until the user requests one, so the letter stage has
    // consumed nothing and neither Agent behind it has been called.
    assert.equal(jobs[1001].letter, null);
    assert.equal(jobs[1002].letter, null);
    assert.equal(jobs[1001].apply_state, null);
    assert.equal(jobs[1002].apply_state, null);
    assert.deepEqual(data.agent_calls, [{ role: "scorer", runner: "claude", ok: true, count: 3 }]);
  } else {
    assert.deepEqual([jobs[1002].apply_state, jobs[1002].letter.status, jobs[1002].letter.rounds], ["pending", "approved", 1]);
    assert.deepEqual([jobs[1001].apply_state, jobs[1001].letter.status, jobs[1001].letter.rounds], [null, "failed", 3]);
    for (const id of [1001, 1002]) {
      assert.equal(jobs[id].letter.runner_draft, "claude");
      assert.equal(jobs[id].letter.runner_review, "codex");
      assert.equal(jobs[id].letter.has_name_placeholder, true);
      assert.equal(jobs[id].letter.has_contact_placeholder, true);
    }
    assert.equal(jobs[1001].letter.review_entries, 3);
    assert.equal(jobs[1002].letter.review_entries, 1);
    assert.deepEqual(data.agent_calls, [
      { role: "drafter", runner: "claude", ok: true, count: 4 },
      { role: "reviewer", runner: "codex", ok: true, count: 4 },
      { role: "scorer", runner: "claude", ok: true, count: 3 },
    ]);
  }

  // runs record fetch facts only; the worker's filter/score/letter never touch them.
  const firstStats = { errors: 0, fetched: 4, new: 4, queries: 3 };
  const chronological = [...data.runs].reverse();
  assert.deepEqual(chronological[0].Stats, firstStats);
  assert.equal(chronological[0].Trigger, "manual-cli");
  assert.ok(chronological[0].FinishedAt);
  assert.equal(chronological[0].Error, null);
  if (phase === "repeat") {
    assert.ok(chronological.length >= 2);
    // The rerun re-fetches the same detail pages but inserts nothing new.
    assert.deepEqual(chronological[1].Stats, { errors: 0, fetched: 4, new: 0, queries: 3 });
  } else {
    assert.equal(chronological.length, 1);
  }
  process.exit(0);
}

if (mode === "api") {
  const data = readJSON();
  assert.equal(data.items.length, 4);
  const jobs = Object.fromEntries(data.items.map((job) => [job.title, job]));
  const ready = jobs["Verification ready hybrid backend engineer"];
  const failed = jobs["Verification failure remote platform engineer"];
  const low = jobs["Verification low score cloud engineer"];
  const unfit = jobs["Verification intern platform engineer"];
  assert.deepEqual([ready.score_total, ready.verdict, ready.letter_state], [90, "recommended", "ready"]);
  assert.deepEqual([failed.score_total, failed.verdict, failed.letter_state], [80, "recommended", "failed"]);
  assert.deepEqual([low.score_total, low.verdict], [60, "not_recommended"]);
  assert.equal(low.letter_state ?? null, null);
  assert.deepEqual([unfit.score_total, unfit.verdict], [null, "unfit"]);
  assert.equal(unfit.letter_state ?? null, null);
  process.exit(0);
}

if (mode === "api-filter") {
  const data = readJSON();
  if (phase === "source") assert.equal(data.items.length, 4);
  if (phase === "process") {
    assert.equal(data.items.length, 1);
    assert.equal(data.items[0].title, "Verification ready hybrid backend engineer");
  }
  if (phase === "apply") {
    assert.equal(data.items.length, 1);
    assert.equal(data.items[0].apply_state, "pending");
  }
  if (phase === "verdict") {
    // The recommended verdict spans both letter outcomes.
    assert.deepEqual(data.items.map(({ title }) => title).sort(), [
      "Verification failure remote platform engineer",
      "Verification ready hybrid backend engineer",
    ]);
    for (const job of data.items) assert.equal(job.verdict, "recommended");
  }
  if (phase === "queue") assert.deepEqual(data.items, []);
  process.exit(0);
}

if (mode === "api-detail") {
  const data = readJSON();
  if (phase === "ready") {
    assert.equal(data.title, "Verification ready hybrid backend engineer");
    assert.equal(data.description, "Build Go backend services");
    assert.equal(data.remote_type, "hybrid");
    assert.deepEqual([data.verdict, data.letter_state], ["recommended", "ready"]);
    assert.deepEqual([data.score.hard_skill, data.score.domain, data.score.seniority, data.score.condition, data.score.direction, data.score.total], [90, 90, 90, 90, 90, 90]);
    assert.equal(data.score.reason, "合成核准情境");
    assert.equal(data.letter.status, "approved");
    assert.equal(data.letter.rounds, 1);
    assert.equal(data.letter.content, "我使用 Go 建立可靠服務。[你的姓名][你的聯絡方式]");
    assert.deepEqual(data.status_events.map(({ axis, from_state, to_state }) => `${axis}:${from_state}->${to_state}`), ["process:->new", "process:new->queued", "process:queued->shortlisted", "process:shortlisted->letter_requested", "process:letter_requested->letter_ready", "apply:->pending"]);
  }
  if (phase === "failed") {
    assert.equal(data.title, "Verification failure remote platform engineer");
    assert.equal(data.process_state, "letter_failed");
    assert.deepEqual([data.verdict, data.letter_state], ["recommended", "failed"]);
    assert.equal(data.score.total, 80);
    assert.equal(data.letter.status, "failed");
    assert.equal(data.letter.rounds, 3);
  }
  process.exit(0);
}

if (mode === "capture-list") {
  // The list path is LLM-free and synchronous: a title that hits an exclude
  // keyword is marked unfit at once, the rest enter the queue as discovered.
  const data = readJSON();
  const items = Object.fromEntries(data.items.map((item) => [item.external_id, item]));
  const excluded = items.v5intern;
  const discovered = items.v5senior;
  assert.ok(excluded, "excluded list item absent from list capture");
  assert.ok(discovered, "discovered list item absent from list capture");
  assert.equal(excluded.verdict, "unfit");
  assert.equal(excluded.process_state, "filtered_out");
  assert.deepEqual(excluded.filter_hits, ["exclude_title_keywords"]);
  assert.equal(excluded.score_total ?? null, null);
  assert.equal(discovered.verdict, "pending_detail");
  assert.equal(discovered.process_state, "discovered");
  if (phase === "repeat") {
    // A list already in the database comes back at its current verdict without
    // being re-created or re-run.
    assert.equal(excluded.created, false);
    assert.equal(discovered.created, false);
  } else {
    assert.equal(excluded.created, true);
    assert.equal(discovered.created, true);
  }
  process.exit(0);
}

if (mode === "capture-job") {
  // A fresh detail passes screening synchronously and comes back pending; the
  // worker scores it out of band, so this path never calls an Agent itself.
  const data = readJSON();
  assert.equal(data.cached, false);
  assert.equal(data.verdict, "pending_score");
  assert.equal(data.process_state, "queued");
  assert.equal(data.score ?? null, null);
  process.exit(0);
}

throw new Error(`unknown assertion mode: ${mode}`);
