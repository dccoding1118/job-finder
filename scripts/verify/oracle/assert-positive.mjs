import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs";

const [mode, file, phase = "base"] = process.argv.slice(2);
if (!mode || !file) throw new Error("usage: assert-positive.mjs <schema|source|snapshot|live-snapshot|api|api-filter|api-detail|capture-list|capture-job|list-marks|cake-detail|group|duplicates|no-alias|worker> <file> [phase]");

const sha256 = (value) => crypto.createHash("sha256").update(value).digest("hex");
const readJSON = () => JSON.parse(fs.readFileSync(file, "utf8"));
const REVISION = /^sha256:[a-f0-9]{64}$/;

// The screening results table joined the schema with the split hard/soft gates,
// the settings table with the runtime switches and the letter attempts table with
// the generation history, so every snapshot mode checks the same eleven-table
// contract.
function assertSchema(data) {
  assert.equal(data.schema_version, 10);
  assert.equal(data.journal_mode.toLowerCase(), "wal");
  assert.equal(data.foreign_keys, true);
  assert.deepEqual(data.tables, ["agent_calls", "filter_results", "job_dupe_candidates", "job_groups", "jobs", "letter_attempts", "letters", "runs", "scores", "settings", "status_events"]);
}

if (mode === "schema") {
  const data = readJSON();
  assertSchema(data);
  assert.deepEqual(data.jobs, []);
  process.exit(0);
}

if (mode === "schema-migrated") {
  const data = readJSON();
  assertSchema(data);
  assert.ok(data.jobs.length > 0);
  process.exit(0);
}

if (mode === "agent-total") {
  const data = readJSON();
  const calls = (data.agent_calls || []).filter(({ role }) => phase === "base" || role === phase);
  process.stdout.write(String(calls.reduce((sum, call) => sum + call.count, 0)));
  process.exit(0);
}

if (mode === "profile-response") {
  const data = readJSON();
  assert.equal(data.status, "ready");
  assert.match(data.filter_revision, REVISION);
  assert.match(data.score_revision, REVISION);
  // A soft-rule-only edit must not disturb the screening revision.
  if (phase === "intents") assert.deepEqual([data.filter_changed, data.score_changed], [false, true]);
  else assert.equal(data.score_changed, phase !== "same");
  assert.equal(data.activation, undefined);
  process.exit(0);
}

if (mode === "profile-reprocess-response") {
  const data = readJSON();
  assert.equal(data.status, "queued");
  assert.match(data.filter_revision, REVISION);
  assert.match(data.score_revision, REVISION);
  const activation = data.activation || {};
  for (const key of ["partial_screened", "refiltered", "requeued", "protected", "unchanged"]) assert.ok(Number.isInteger(activation[key]) && activation[key] >= 0);
  if (phase === "change") {
    // A hard-rule change sends screened jobs back through the screen itself,
    // while letter history stays protected from any reprocess.
    assert.ok(activation.refiltered > 0);
    assert.ok(activation.protected > 0);
  }
  process.exit(0);
}

if (mode === "profile-race") {
  const data = readJSON();
  assert.equal(data.schema_version, 10);
  const revisions = new Set(data.agent_calls.map(({ score_revision }) => score_revision).filter(Boolean));
  assert.ok(revisions.size >= 2, "agent audit did not retain multiple score revisions");
  const currentScores = data.jobs.filter((job) => job.score && job.score_revision === job.score.score_revision);
  assert.ok(currentScores.length > 0, "no current Score matches its Job score revision");
  for (const job of currentScores) assert.match(job.score_revision, REVISION);
  process.exit(0);
}

// screened-intact asserts the jobs a soft-rule change must not touch: a
// structurally rejected job keeps both its state and its screening record.
if (mode === "screened-intact") {
  const data = readJSON();
  const rejected = data.jobs.find(({ external_id }) => external_id === "1000");
  assert.ok(rejected, "the structurally rejected fixture is absent");
  assert.equal(rejected.process_state, "filtered_out");
  assert.equal(rejected.filter_outcome, "fail");
  assert.deepEqual(rejected.filter_hits, ["exclude_title_keywords", "salary_floor"]);
  process.exit(0);
}

if (mode === "source") {
  const requests = fs.readFileSync(file, "utf8").trim().split("\n").filter(Boolean).map(JSON.parse);
  assert.equal(requests.length, 9);
  assert.equal(requests[0].path, "/robots.txt");
  const lists = requests.filter(({ path }) => path === "/api/v4/jobs");
  assert.equal(lists.length, 3);
  assert.deepEqual(lists.map(({ query }) => query["term[]"]), [["cloud", "platform"], ["backend", "Go"], ["Kubernetes", "reliability"]]);
  for (const list of lists) {
    assert.deepEqual({ method: list.method, page: list.query.page }, { method: "GET", page: ["1"] });
    assert.match(list.user_agent, /^jobfinder\/1\.0/);
    assert.equal(list.referer, "");
  }
  assert.deepEqual(requests.filter(({ path }) => path.startsWith("/jobs/")).map(({ method, path }) => ({ method, path })), [1000, 1001, 1002, 1003, 1004].map((id) => ({ method: "GET", path: `/jobs/${id}` })));
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
    assert.ok(["approved", "finalized"].includes(lettered[0].letter.status));
    assert.ok(data.agent_calls.some(({ role, ok }) => role === "scorer" && ok));
    assert.ok(data.agent_calls.some(({ role, ok }) => role === "drafter" && ok));
    // A finalized letter is the last round's version, kept without a final review,
    // so only an approved letter proves the reviewer ran.
    if (lettered[0].letter.status === "approved") {
      assert.ok(data.agent_calls.some(({ role, ok }) => role === "reviewer" && ok));
    }
  }
  process.stdout.write(JSON.stringify({ jobs: data.jobs.length, ids_sha256: sha256([...ids].sort().join("\n")), scored: scored.length, lettered: lettered.length }));
  process.exit(0);
}

// The five synthetic fixtures. process_state and letter change with the phase;
// every other field is fixed by the production adapter and fake Agent, so it is
// asserted identically in every phase.
const FIXTURES = {
  1000: { title: "Verification intern platform engineer", company: "Example Learning", description: "Training cloud platform work", salary: [60000, 70000], remote: "onsite" },
  1001: { title: "Verification failure remote platform engineer", company: "Example Platform", description: "Build Go & cloud platform services", salary: [100000, 120000], remote: "remote" },
  1002: { title: "Verification ready hybrid backend engineer", company: "Example Services", description: "Build Go backend services", salary: [110000, 130000], remote: "hybrid" },
  1003: { title: "Verification low score cloud engineer", company: "Example Operations", description: "Maintain cloud operations services", salary: [90000, 100000], remote: "onsite" },
  1004: { title: "Verification unknown salary platform engineer", company: "Example Ventures", description: "Operate cloud platform services. Contact [EMAIL] or [PHONE]", salary: [null, null], remote: "onsite" },
};

function assertAgentCalls(actual, expected) {
  // Screening is audited against the filter revision and scoring against the
  // score revision; a letter call carries both.
  for (const call of actual) {
    if (call.role !== "scorer") assert.match(call.filter_revision, REVISION);
    if (call.role !== "filter") assert.match(call.score_revision, REVISION);
  }
  assert.deepEqual(actual.map(({ filter_revision: _f, score_revision: _s, ...call }) => call), expected);
}

// letterConsumed is true once the user has requested the two letters and the
// letter stage has been driven; base is the pre-request state.
function processStateOf(id, letterConsumed) {
  if (id === "1000") return "filtered_out";
  if (id === "1003" || id === "1004") return "scored";
  if (!letterConsumed) return "shortlisted";
  return id === "1002" ? "letter_ready" : "letter_failed";
}

function transitionsOf(id, letterConsumed) {
  if (id === "1000") return ["process:->new", "process:new->filtered_out"];
  if (id === "1003" || id === "1004") return ["process:->new", "process:new->queued", "process:queued->scored"];
  const base = ["process:->new", "process:new->queued", "process:queued->shortlisted"];
  if (!letterConsumed) return base;
  if (id === "1002") return [...base, "process:shortlisted->letter_requested", "process:letter_requested->letter_ready", "apply:->pending"];
  return [...base, "process:shortlisted->letter_requested", "process:letter_requested->letter_failed"];
}

// The structural rules the Profile actually activates, in the order the screen
// evaluates them. Their verdicts are the whole of a structurally rejected job's
// screening record, and the prefix of every semantically screened one.
const STRUCTURAL_RULES = ["exclude_title_keywords", "exclude_companies", "locations", "remote", "salary_floor", "exclude_description_keywords"];

function structuralVerdicts(conditions) {
  const structural = conditions.filter(({ category }) => category === "other");
  const rules = structural.map(({ rule }) => rule);
  // A rule with nothing to compare against is skipped rather than recorded — a
  // fully remote job has no commute to judge — so the record is the ordered
  // subset of the activated rules, not always all of them.
  assert.deepEqual(rules, STRUCTURAL_RULES.filter((rule) => rules.includes(rule)));
  assert.ok(rules.includes("salary_floor") && rules.includes("exclude_title_keywords"));
  return Object.fromEntries(structural.map(({ rule, verdict }) => [rule, verdict]));
}

if (mode === "snapshot") {
  const data = readJSON();
  const letterConsumed = phase === "lettered" || phase === "repeat";
  assertSchema(data);
  assert.equal(data.jobs.length, 5);
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
    assert.match(job.filter_revision, REVISION);
    const content = [expected.title, expected.description, ...expected.salary.map((value) => (value === null ? "" : String(value))), "Taipei", expected.remote].join("\n");
    assert.equal(job.content_hash, sha256(content));
    assert.deepEqual(job.events.map(({ axis, from_state, to_state }) => `${axis}:${from_state}->${to_state}`), transitionsOf(id, letterConsumed));
  }

  // #1000 is rejected by the structural pass alone: it never reaches the Agent,
  // so its screening record holds nothing but the deterministic rules.
  assert.equal(jobs[1000].filter_outcome, "fail");
  assert.deepEqual(jobs[1000].filter_hits, ["exclude_title_keywords", "salary_floor"]);
  assert.deepEqual(jobs[1000].filter_conditions.filter(({ category }) => category !== "other"), []);
  assert.equal(jobs[1000].score, null);
  assert.equal(jobs[1000].letter, null);
  assert.equal(jobs[1000].score_revision, null);

  // #1004 discloses no salary and leaves one required condition undecidable, yet
  // its JD is complete: neither absence can be resolved by waiting, so screening
  // records both as undecided and still sends the job on to scoring. Nothing was
  // rejected, so it names no reason.
  assert.equal(jobs[1004].filter_outcome, "pass");
  assert.deepEqual(jobs[1004].filter_hits, []);
  const unknownRules = structuralVerdicts(jobs[1004].filter_conditions);
  assert.equal(unknownRules.salary_floor, "unknown");
  assert.deepEqual(Object.entries(unknownRules).filter(([, verdict]) => verdict !== "pass").map(([rule]) => rule), ["salary_floor"]);
  const undecided = jobs[1004].filter_conditions.filter(({ category }) => category !== "other");
  assert.deepEqual(undecided.map(({ kind, verdict }) => `${kind}:${verdict}`), ["required:unknown"]);
  assert.equal(jobs[1004].score.total, 70);
  assert.equal(jobs[1004].score.reason_sha256, sha256("合成資訊不足情境"));

  // The three fully screened jobs each carry the Agent's condition breakdown, and
  // the unmet bonus condition proves a bonus never contributes to a rejection.
  for (const id of [1001, 1002, 1003]) {
    assert.equal(jobs[id].filter_outcome, "pass");
    const semantic = jobs[id].filter_conditions.filter(({ category }) => category !== "other");
    assert.deepEqual(semantic.map(({ kind, verdict }) => `${kind}:${verdict}`), ["required:pass", "bonus:fail"]);
    for (const verdict of Object.values(structuralVerdicts(jobs[id].filter_conditions))) assert.equal(verdict, "pass");
  }

  assert.equal(jobs[1003].score.total, 60);
  assert.deepEqual([jobs[1003].score.content_fit, jobs[1003].score.benefit_fit, jobs[1003].score.bonus_fit, jobs[1003].score.industry_fit], [60, 60, 60, 60]);
  assert.equal(jobs[1003].score.runner, "claude");
  assert.equal(jobs[1003].score.score_revision, jobs[1003].score_revision);
  assert.equal(jobs[1003].score.reason_sha256, sha256("合成低分情境"));
  assert.equal(jobs[1003].letter, null);
  assert.equal(jobs[1001].score.total, 80);
  assert.equal(jobs[1001].score.score_revision, jobs[1001].score_revision);
  assert.equal(jobs[1001].score.reason_sha256, sha256("合成重試情境"));
  assert.equal(jobs[1002].score.total, 90);
  assert.deepEqual([jobs[1002].score.content_fit, jobs[1002].score.benefit_fit, jobs[1002].score.bonus_fit, jobs[1002].score.industry_fit], [90, 90, 90, 90]);
  assert.equal(jobs[1002].score.reason_sha256, sha256("合成核准情境"));
  assert.equal(jobs[1002].score.score_revision, jobs[1002].score_revision);

  if (!letterConsumed) {
    // No letter is drafted until the user requests one, so the letter stage has
    // consumed nothing and neither Agent behind it has been called.
    assert.equal(jobs[1001].letter, null);
    assert.equal(jobs[1002].letter, null);
    assert.equal(jobs[1001].apply_state, null);
    assert.equal(jobs[1002].apply_state, null);
    assertAgentCalls(data.agent_calls, [
      { role: "filter", runner: "claude", ok: true, count: 4 },
      { role: "scorer", runner: "claude", ok: true, count: 4 },
    ]);
  } else {
    assert.deepEqual([jobs[1002].apply_state, jobs[1002].letter.status, jobs[1002].letter.rounds], ["pending", "approved", 1]);
    // #1001 exhausts every reviewer runner on its first round. It produced no
    // usable letter, so it records none: the state and the audited calls carry the
    // outcome, and the user decides whether to ask for another attempt.
    assert.equal(jobs[1001].letter, null);
    assert.equal(jobs[1001].apply_state, null);
    assert.equal(jobs[1002].letter.runner_draft, "claude");
    assert.equal(jobs[1002].letter.runner_review, "codex");
    assert.equal(jobs[1002].letter.has_name_placeholder, true);
    assert.equal(jobs[1002].letter.has_contact_placeholder, true);
    assert.equal(jobs[1002].letter.score_revision, jobs[1002].score_revision);
    assert.equal(jobs[1002].letter.filter_revision, jobs[1002].filter_revision);
    assert.equal(jobs[1002].letter.review_entries, 1);
    assertAgentCalls(data.agent_calls, [
      { role: "drafter", runner: "claude", ok: true, count: 2 },
      { role: "filter", runner: "claude", ok: true, count: 4 },
      { role: "reviewer", runner: "claude", ok: false, count: 1 },
      { role: "reviewer", runner: "codex", ok: false, count: 2 },
      { role: "reviewer", runner: "codex", ok: true, count: 1 },
      { role: "scorer", runner: "claude", ok: true, count: 4 },
    ]);
  }

  // runs record fetch facts only; the worker's filter/score/letter never touch them.
  const firstStats = { errors: 0, fetched: 5, new: 5, queries: 3 };
  const chronological = [...data.runs].reverse();
  assert.deepEqual(chronological[0].Stats, firstStats);
  assert.equal(chronological[0].Trigger, "manual-cli");
  assert.ok(chronological[0].FinishedAt);
  assert.equal(chronological[0].Error, null);
  if (phase === "repeat") {
    assert.ok(chronological.length >= 2);
    // The rerun re-fetches the same detail pages but inserts nothing new.
    assert.deepEqual(chronological[1].Stats, { errors: 0, fetched: 5, new: 0, queries: 3 });
  } else {
    assert.equal(chronological.length, 1);
  }
  process.exit(0);
}

if (mode === "api") {
  const data = readJSON();
  assert.equal(data.items.length, 5);
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
  // The undisclosed-salary job was screened through and scored like any other:
  // an undecidable condition on a complete JD is not a reason to withhold it.
  const undisclosed = jobs["Verification unknown salary platform engineer"];
  assert.deepEqual([undisclosed.score_total, undisclosed.verdict], [70, "not_recommended"]);
  assert.equal(undisclosed.letter_state ?? null, null);
  process.exit(0);
}

if (mode === "api-filter") {
  const data = readJSON();
  if (phase === "source") assert.equal(data.items.length, 5);
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
  // 待看 is carried entirely by list items still waiting for their full text, so
  // the queue is empty until one is captured.
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
    assert.deepEqual([data.score.content_fit, data.score.benefit_fit, data.score.bonus_fit, data.score.industry_fit, data.score.total], [90, 90, 90, 90, 90]);
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
    assert.equal(data.letter, null);
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
  // A fresh detail clears the structural conditions synchronously and comes back
  // pending. The semantic half of the screen costs a token call, so the capture
  // request never waits for it: the job rests in `new` until the worker screens
  // and then scores it, and this path calls no Agent of its own.
  const data = readJSON();
  assert.equal(data.cached, false);
  assert.equal(data.verdict, "pending_screen");
  assert.equal(data.process_state, "new");
  assert.equal(data.score ?? null, null);
  process.exit(0);
}

// The Cake fixtures. Every one of them is keyed by the company and job path
// segments Cake builds an identity out of, which is what the list capture and
// the detail capture of the same listing have to agree on.
const LIST_MARKS = {
  next: {
    "example-services/backend-intern-engineer": { verdict: "unfit", process_state: "filtered_out", filter_hits: ["exclude_title_keywords"] },
    // The senior listing is the 104 job under another name, so the mark it comes
    // back with is the canonical copy's, not a second opinion of its own.
    "beta-co/senior-backend-engineer": { verdict: "pending_detail", process_state: "discovered", filter_hits: null },
    "cake-only-labs/platform-reliability-engineer": { verdict: "pending_detail", process_state: "discovered", filter_hits: null },
  },
  // The same page captured again once the pipeline has run: the marks are the
  // verdicts already reached, and nothing is created a second time.
  "next-again": {
    "example-services/backend-intern-engineer": { verdict: "unfit", process_state: "filtered_out", filter_hits: ["exclude_title_keywords"] },
    "beta-co/senior-backend-engineer": { verdict: "recommended", process_state: "shortlisted", filter_hits: null },
    "cake-only-labs/platform-reliability-engineer": { verdict: "recommended", process_state: "shortlisted", filter_hits: null },
  },
  dom: {
    "delta-works/backend-engineer": { verdict: "pending_detail", process_state: "discovered", filter_hits: null },
    "delta-works/backend-engineer-platform": { verdict: "pending_detail", process_state: "discovered", filter_hits: null },
  },
  "grey-base": {
    v6greybase: { verdict: "pending_detail", process_state: "discovered", filter_hits: null },
    v6ignorebase: { verdict: "pending_detail", process_state: "discovered", filter_hits: null },
  },
  grey: {
    "grey-labs/data-platform-engineer-core": { verdict: "pending_detail", process_state: "discovered", filter_hits: null },
    "ignore-works/mobile-platform-engineer-core": { verdict: "pending_detail", process_state: "discovered", filter_hits: null },
  },
};

if (mode === "list-marks") {
  const expected = LIST_MARKS[phase];
  if (!expected) throw new Error(`unknown list mark phase: ${phase}`);
  const data = readJSON();
  const items = Object.fromEntries(data.items.map((item) => [item.external_id, item]));
  assert.deepEqual(Object.keys(items).sort(), Object.keys(expected).sort());
  for (const [id, want] of Object.entries(expected)) {
    const item = items[id];
    assert.deepEqual([item.verdict, item.process_state], [want.verdict, want.process_state], `cake list item ${id}`);
    assert.deepEqual(item.filter_hits ?? null, want.filter_hits, `cake list hits of ${id}`);
    assert.equal(item.created, !phase.endsWith("-again"));
    if (want.process_state === "discovered" || want.process_state === "filtered_out") assert.equal(item.score_total ?? null, null);
  }
  process.exit(0);
}

// The two Cake detail fixtures. One carries a metadata area the parser can read
// a place, a monthly salary, and a remote arrangement out of; the other carries
// none of the three, and the point of it is that they come back undecided
// rather than guessed.
const CAKE_DETAIL = {
  meta: {
    url: "https://www.cake.me/companies/beta-co/jobs/senior-backend-engineer",
    description: "職缺描述\nBuild Go backend and cloud platform services\n\n職務需求\nFamiliar with Go, Kubernetes and cloud platforms",
    location: "台北市",
    salary: [120000, 150000],
    remote_type: "hybrid",
    process_state: "merged",
  },
  nometa: {
    url: "https://www.cake.me/companies/cake-only-labs/jobs/platform-reliability-engineer",
    description: "職缺描述\nOperate cloud platform reliability services\n\n職務需求\nExperience with Go and Kubernetes",
    location: "unknown",
    salary: [null, null],
    remote_type: "unknown",
  },
};

if (mode === "cake-detail") {
  const expected = CAKE_DETAIL[phase];
  if (!expected) throw new Error(`unknown cake detail phase: ${phase}`);
  const data = readJSON();
  assert.equal(data.source, "cake");
  assert.equal(data.url, expected.url);
  // Both JD blocks are stored under their own heading: the scorer has to be able
  // to read requirements as requirements.
  assert.equal(data.description, expected.description);
  assert.equal(data.location, expected.location);
  assert.deepEqual([data.salary_min, data.salary_max], expected.salary);
  assert.equal(data.remote_type, expected.remote_type);
  if (expected.process_state) assert.equal(data.process_state, expected.process_state);
  process.exit(0);
}

// group reads one Job detail and asserts the cross-source group it belongs to.
if (mode === "group") {
  const data = readJSON();
  const group = data.group;
  assert.ok(group, "job detail carries no group");
  if (phase === "merged") {
    // The 104 copy carries the processing; the Cake copy is the same listing on
    // another platform and holds no verdict of its own.
    assert.equal(data.source, "104");
    assert.equal(group.canonical_job_id, data.id);
    const members = Object.fromEntries(group.members.map((member) => [member.source, member]));
    assert.deepEqual(Object.keys(members).sort(), ["104", "cake"]);
    assert.equal(members["104"].merged, false);
    assert.equal(members["104"].job_id, data.id);
    assert.equal(members.cake.merged, true);
    assert.equal(members.cake.external_id, "beta-co/senior-backend-engineer");
    assert.equal(members.cake.url, "https://www.cake.me/companies/beta-co/jobs/senior-backend-engineer");
    process.stdout.write(String(members.cake.job_id));
  }
  if (phase === "alias-merged") {
    // The alias records what it was before the merge and which job now carries
    // it, which is the whole of what an unmerge restores it from.
    assert.equal(data.source, "cake");
    assert.equal(data.process_state, "merged");
    assert.equal(data.verdict ?? "", "");
    const merge = data.status_events.filter(({ to_state }) => to_state === "merged").pop();
    assert.ok(merge, "the alias records no merge event");
    assert.equal(merge.from_state, "discovered");
    const note = /^merged into job (\d+) from discovered$/.exec(merge.note ?? "");
    assert.ok(note, `merge note is not readable: ${merge.note}`);
    assert.equal(Number(note[1]), group.canonical_job_id);
  }
  if (phase === "alias-restored") {
    // An unmerge puts the alias back in the state and the standalone group it
    // had before, and it is a job of its own again.
    assert.equal(data.source, "cake");
    assert.equal(data.process_state, "discovered");
    assert.equal(group.canonical_job_id, data.id);
    assert.deepEqual(group.members.map(({ source, merged }) => [source, merged]), [["cake", false]]);
  }
  if (phase === "canonical-restored") {
    // The copy that carried the work keeps all of it: an unmerge costs a click,
    // not a score or a letter.
    assert.equal(data.source, "104");
    assert.ok(data.score, "the canonical job lost its score");
    assert.deepEqual(group.members.map(({ source }) => source), ["104"]);
  }
  if (phase === "candidate-merged") {
    assert.equal(data.source, "104");
    assert.equal(group.canonical_job_id, data.id);
    const members = Object.fromEntries(group.members.map((member) => [member.source, member]));
    assert.deepEqual(Object.keys(members).sort(), ["104", "cake"]);
    assert.equal(members.cake.merged, true);
    process.stdout.write(String(members.cake.job_id));
  }
  if (phase === "standalone") {
    // A pair the user ruled apart stays two jobs, and is never suggested again.
    assert.equal(group.canonical_job_id, data.id);
    assert.equal(group.members.length, 1);
    assert.equal(group.duplicate_candidate_count, 0);
  }
  if (phase === "same-source") {
    // Two listings on one platform are two openings: they are never grouped.
    assert.equal(group.canonical_job_id, data.id);
    assert.deepEqual(group.members.map(({ source }) => source), ["cake"]);
    assert.equal(group.duplicate_candidate_count, 0);
  }
  process.exit(0);
}

// duplicates asserts the grey-zone pairs left for the user. Titles that are
// similar but not equal never merge on their own, whichever way they are then
// decided.
if (mode === "duplicates") {
  const data = readJSON();
  const pairs = data.items.map((candidate) => {
    assert.equal(candidate.reason, "title_similar");
    assert.ok(candidate.similarity >= 0.6 && candidate.similarity < 1, `candidate ${candidate.id} similarity ${candidate.similarity}`);
    const sources = [candidate.a.source, candidate.b.source].sort();
    assert.deepEqual(sources, ["104", "cake"]);
    return [candidate.id, [candidate.a.company_name, candidate.b.company_name].some((name) => name.startsWith("Grey Labs")) ? "grey" : "ignore"];
  });
  const byKind = Object.fromEntries(pairs.map(([id, kind]) => [kind, id]));
  if (phase === "pending") {
    assert.deepEqual(Object.keys(byKind).sort(), ["grey", "ignore"]);
    process.stdout.write(`${byKind.grey} ${byKind.ignore}`);
  }
  if (phase === "after-merge") {
    assert.deepEqual(Object.keys(byKind), ["ignore"]);
    process.stdout.write(String(byKind.ignore));
  }
  if (phase === "after-ignore" || phase === "empty") assert.deepEqual(data.items, []);
  process.exit(0);
}

// no-alias proves a collection the user reads never carries an alias: a merged
// job is listed nowhere, which is what keeps one listing from appearing twice.
if (mode === "no-alias") {
  const data = readJSON();
  for (const item of data.items) {
    assert.notEqual(item.process_state, "merged");
    assert.notEqual(String(item.id), String(phase));
  }
  process.exit(0);
}

// live-fingerprint reduces the stored jobs to what a repeated fetch of an
// unchanged source must reproduce exactly: the identity, the content hash the
// change detection keys on, and the processing state that hash decides. A live
// source whose pages carry per-request state would otherwise reset every job to
// `new` on each run and pay for the whole batch again.
if (mode === "live-fingerprint") {
  const data = readJSON();
  const rows = data.jobs
    .map((job) => [job.source, job.external_id, job.content_hash ?? "", job.process_state].join("|"))
    .sort();
  process.stdout.write(`${rows.length}:${sha256(rows.join("\n"))}`);
  process.exit(0);
}

if (mode === "queued-count") {
  const data = readJSON();
  process.stdout.write(String(data.jobs.filter(({ process_state }) => process_state === "queued").length));
  process.exit(0);
}

// worker-order reads the service log and asserts the order the worker consumed
// the two stages in: everything already screened is scored first, and a single
// pass is bounded by the batch size, so a queue larger than it is drained over
// several passes before anything new is screened.
if (mode === "worker-order") {
  const lines = fs.readFileSync(file, "utf8").split("\n");
  const picks = [];
  for (const line of lines) {
    if (!line.includes("stage picked jobs")) continue;
    const stage = /\bstage=(score|filter)\b/.exec(line);
    const jobs = /\bjobs=(\d+)/.exec(line);
    assert.ok(stage && jobs, `a stage pick reported no stage or job count: ${line}`);
    picks.push([stage[1], Number(jobs[1])]);
  }
  const firstFilter = picks.findIndex(([stage]) => stage === "filter");
  assert.ok(firstFilter > 0, "the worker screened before it scored anything");
  const scored = picks.slice(0, firstFilter);
  for (const [stage] of scored) assert.equal(stage, "score");
  assert.equal(scored[0][1], 50, "the first scoring pass did not take a full batch");
  assert.ok(scored.length >= 2, "a queue larger than one batch was not picked up again");
  assert.equal(scored.reduce((total, [, jobs]) => total + jobs, 0), 55);
  assert.equal(picks[firstFilter][1], 1);
  process.exit(0);
}

throw new Error(`unknown assertion mode: ${mode}`);
