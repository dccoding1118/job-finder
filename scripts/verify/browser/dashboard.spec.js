const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const dashboardPath = `file://${path.join(__dirname, "../../../extension/dashboard/index.html")}`;
const optionsPath = `file://${path.join(__dirname, "../../../extension/options/index.html")}`;

test("Side Panel loads, filters, shows a job, persists theme, updates apply state, and starts a run", async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 900 });
  await page.addInitScript(() => {
    const jobs = [{ id: 2, source: "yourator", url: "https://www.yourator.co/jobs/2", title: "Synthetic job", company_name: "Example", salary_min: 100000, salary_max: 120000, location: "Taipei", score_total: 90.456, score_stale: true, score_result_revision: `sha256:${"9".repeat(64)}`, filter_result_revision: `sha256:${"9".repeat(64)}`, process_state: "letter_ready", apply_state: "pending", verdict: "recommended", letter_state: "ready" }];
    window.__calls = [];
    window.__storage = { theme: "light" };
    window.chrome = {
      storage: { local: {
        get: (defaults, callback) => callback({ ...defaults, ...window.__storage }),
        set: (values, callback) => { Object.assign(window.__storage, values); callback?.(); },
      } },
      runtime: {
        onMessage: { addListener: () => {} },
        openOptionsPage: () => { window.__openedOptions = true; return Promise.resolve(); },
        sendMessage: (request) => {
          window.__calls.push(request);
		  if (request.type === "profile-api" && request.path === "/api/v1/profile/reprocess") return Promise.resolve({ ok: true, data: { status: "queued", activation: { requeued: 1 } } });
		  if (request.type === "profile-api") return Promise.resolve({ ok: true, etag: '"sha256:file"', data: { status: "ready", filter_revision: `sha256:${"a".repeat(64)}`, score_revision: `sha256:${"c".repeat(64)}`, summary: { total_years: 8, skill_count: 3, experience_count: 1, directions: ["cloud architecture"] }, issues: [], reprocess_estimate: { requeued: 1, protected: 1 } } });
          if (request.type === "current-page") return Promise.resolve({ ok: true, context: { kind: "unsupported", status: "unsupported" } });
          if (request.path.startsWith("/api/v1/jobs?")) return Promise.resolve({ ok: true, data: { items: jobs } });
          if (request.path === "/api/v1/jobs/2") return Promise.resolve({ ok: true, data: { ...jobs[0], description: "Synthetic description", score: { content_fit: 90, benefit_fit: 90, bonus_fit: 90, industry_fit: 90, total: 90, reason: "Synthetic" }, letter: { status: "approved", content: "Synthetic letter" }, status_events: [] } });
          if (request.path === "/api/v1/queue") return Promise.resolve({ ok: true, data: { items: [] } });
          if (request.path === "/api/v1/runs" && request.method === "POST") return Promise.resolve({ ok: true, data: { status: "started" } });
          if (request.path === "/api/v1/runs") return Promise.resolve({ ok: true, data: { items: [{ started_at: "2026-07-16T00:00:00+08:00", finished_at: null, trigger: "manual-extension", stats: { errors: 0, fetched: 4, new: 0, queries: 3 }, verdicts: { recommended: 2, not_recommended: 1, unfit: 1 }, error: null }] } });
          if (request.path === "/api/v1/jobs/2/apply") return Promise.resolve({ ok: true, data: { ...jobs[0], apply_state: request.body.apply_state, description: "Synthetic description", score: { content_fit: 90, benefit_fit: 90, bonus_fit: 90, industry_fit: 90, total: 90, reason: "Synthetic" }, letter: { status: "approved", content: "Synthetic letter" }, status_events: [] } });
          return Promise.resolve({ ok: true, data: jobs[0] });
        },
      },
    };
    navigator.clipboard = { writeText: () => Promise.resolve() };
  });
  await page.goto(dashboardPath);
  await expect(page.getByRole("status").first()).toHaveText("已連線至 jobfinder API");
  await page.getByRole("tab", { name: /推薦/ }).click();
  await page.getByRole("button", { name: /Synthetic job/ }).click();
  await expect(page.locator("#letter")).toHaveText("Synthetic letter");
  await expect(page.locator("#score")).toContainText("工作內容90");
  await expect(page.locator(".score-total strong")).toHaveText("90");
  await expect(page.locator("#screen-current .revision-badge.is-stale")).toContainText("待重評");
  await page.locator("#theme-toggle").click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  expect(await page.evaluate(() => window.__storage.theme)).toBe("dark");
  await page.getByRole("button", { name: "更新投遞狀態" }).click();
  await page.getByRole("tab", { name: /推薦/ }).click();
  await expect(page.getByRole("button", { name: /Synthetic job/ })).toHaveClass(/is-viewed/);
  await expect(page.getByRole("button", { name: /Synthetic job/ })).toContainText("已看");
  await page.getByText("進階篩選").click();
  await page.locator("#source-filter").selectOption("yourator");
  await page.locator("#process").selectOption("letter_ready");
  await page.getByRole("tab", { name: "系統" }).click();
	await expect(page.locator(".profile-card")).toContainText("年資 8 年");
	await expect(page.getByRole("button", { name: "編輯履歷與求職條件" })).toBeEnabled();
  await expect(page.getByText("每日 08:30（Asia/Taipei）")).toBeVisible();
  await page.getByRole("button", { name: "開啟連線設定" }).click();
  expect(await page.evaluate(() => window.__openedOptions)).toBeTruthy();
  await page.getByRole("button", { name: "更新過時判定職缺" }).click();
  await expect(page.locator("#runs")).toContainText("fetched=4");
  await expect(page.locator("#runs")).toContainText("recommended=2");
  await expect(page.locator("#runs")).not.toContainText("[object Object]");
  await page.getByRole("button", { name: "立即手動抓取" }).click();
  const calls = await page.evaluate(() => window.__calls);
  expect(calls.some((call) => call.path === "/api/v1/jobs/2/apply" && call.body.apply_state === "pending")).toBeTruthy();
  expect(calls.some((call) => call.path === "/api/v1/runs" && call.method === "POST")).toBeTruthy();
  expect(calls.some((call) => call.type === "profile-api" && call.path === "/api/v1/profile/reprocess" && call.method === "POST")).toBeTruthy();
  expect(calls.some((call) => call.path?.includes("source=yourator"))).toBeTruthy();
});

test("a scored job can be reprocessed on its own and the system tab shows processing progress", async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 900 });
  await page.addInitScript(() => {
    const scored = { id: 5, source: "yourator", url: "https://www.yourator.co/jobs/5", title: "Synthetic scored job", company_name: "Example", location: "Taipei", score_total: 55, process_state: "scored", apply_state: "pending", verdict: "not_recommended" };
    const screening = { ...scored, process_state: "new", verdict: "pending_screen", score_total: null };
    window.__calls = [];
    window.chrome = {
      storage: { local: {
        get: (defaults, callback) => callback(defaults),
        set: (values, callback) => callback?.(values),
      } },
      runtime: {
        onMessage: { addListener: () => {} },
        openOptionsPage: () => Promise.resolve(),
        sendMessage: (request) => {
          window.__calls.push(request);
          if (request.type === "profile-api") return Promise.resolve({ ok: true, data: { status: "ready", filter_revision: `sha256:${"a".repeat(64)}`, score_revision: `sha256:${"c".repeat(64)}`, summary: { total_years: 8, skill_count: 3, experience_count: 1, directions: ["cloud architecture"] }, issues: [], reprocess_estimate: {} } });
          if (request.type === "current-page") return Promise.resolve({ ok: true, context: { kind: "unsupported", status: "unsupported" } });
          if (request.path === "/api/v1/jobs/5/reprocess") return Promise.resolve({ ok: true, data: { status: "new", job: { ...screening, description: "Synthetic description", status_events: [] } } });
          if (request.path === "/api/v1/jobs/5") return Promise.resolve({ ok: true, data: { ...scored, description: "Synthetic description", score: { content_fit: 5, benefit_fit: 5, bonus_fit: 5, industry_fit: 5, total: 55, reason: "Synthetic" }, status_events: [] } });
          if (request.path?.startsWith("/api/v1/jobs?")) return Promise.resolve({ ok: true, data: { items: [scored], next_cursor: null } });
          if (request.path === "/api/v1/status") return Promise.resolve({ ok: true, data: { jobs: { queued: 3, new: 1 }, score_budget: { remaining: 0, limited: true }, agent_calls: [{ id: 9, job_id: 5, role: "scorer", runner: "claude", ok: false, duration_ms: 90000, created_at: "2026-07-25T09:12:00+08:00", failure_kind: "reason_too_long", detail: "{\"reason\":\"…\"}" }, { id: 8, job_id: 5, role: "scorer", runner: "claude", ok: true, duration_ms: 8000, created_at: "2026-07-25T09:10:00+08:00" }] } });
          return Promise.resolve({ ok: true, data: { items: [], next_cursor: null } });
        },
      },
    };
  });
  await page.goto(dashboardPath);
  await page.getByRole("tab", { name: /推薦/ }).click();
  await page.getByRole("button", { name: /Synthetic scored job/ }).click();
  await page.getByRole("button", { name: "重新處理這筆職缺" }).click();
  await expect(page.locator("#verdict-label")).toContainText("篩選中");
  await expect(page.getByRole("button", { name: "重新處理這筆職缺" })).toHaveCount(0);
  await page.getByRole("tab", { name: "系統" }).click();
  await expect(page.locator(".system-card").nth(3)).toContainText("評分中");
  await expect(page.locator(".system-card").nth(3)).toContainText("剩 0");
  await expect(page.locator("#agent-calls")).toContainText("理由超過 100 字上限");
  await expect(page.locator("#agent-calls")).toContainText("90s");
  await expect(page.locator("#agent-calls .run-item").nth(1)).toContainText("呼叫成功");
  await expect(page.locator("#agent-calls .run-item").nth(1)).not.toContainText("失敗");
  const calls = await page.evaluate(() => window.__calls);
  expect(calls.some((call) => call.path === "/api/v1/jobs/5/reprocess" && call.method === "POST")).toBeTruthy();
  expect(calls.some((call) => call.path === "/api/v1/status")).toBeTruthy();
});

test("a waiting job is pushed through immediately and the switch stops automatic processing", async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 900 });
  await page.addInitScript(() => {
    const waiting = { id: 6, source: "yourator", url: "https://www.yourator.co/jobs/6", title: "Synthetic waiting job", company_name: "Example", location: "Taipei", score_total: null, process_state: "queued", apply_state: "none", verdict: "pending_score" };
    window.__calls = [];
    window.__auto = false;
    window.chrome = {
      storage: { local: {
        get: (defaults, callback) => callback(defaults),
        set: (values, callback) => callback?.(values),
      } },
      runtime: {
        onMessage: { addListener: () => {} },
        openOptionsPage: () => Promise.resolve(),
        sendMessage: (request) => {
          window.__calls.push(request);
          if (request.type === "profile-api") return Promise.resolve({ ok: true, data: { status: "ready", filter_revision: `sha256:${"a".repeat(64)}`, score_revision: `sha256:${"c".repeat(64)}`, summary: { total_years: 8, skill_count: 3, experience_count: 1, directions: ["cloud architecture"] }, issues: [], reprocess_estimate: {} } });
          if (request.type === "current-page") return Promise.resolve({ ok: true, context: { kind: "unsupported", status: "unsupported" } });
          if (request.path === "/api/v1/jobs/6/process") return Promise.resolve({ ok: true, data: { status: "processing", job: { ...waiting, description: "Synthetic description", status_events: [] } } });
          if (request.path === "/api/v1/jobs/6") return Promise.resolve({ ok: true, data: { ...waiting, description: "Synthetic description", status_events: [] } });
          if (request.path?.startsWith("/api/v1/jobs?")) return Promise.resolve({ ok: true, data: { items: [waiting], next_cursor: null } });
          if (request.path === "/api/v1/settings" && request.method === "PUT") {
            window.__auto = request.body.auto_processing;
            return Promise.resolve({ ok: true, data: { auto_processing: window.__auto, resident_worker: true } });
          }
          if (request.path === "/api/v1/status") return Promise.resolve({ ok: true, data: { jobs: { queued: 3, new: 1 }, score_budget: { remaining: 0, limited: true }, agent_calls: [], settings: { auto_processing: window.__auto, resident_worker: true } } });
          return Promise.resolve({ ok: true, data: { items: [], next_cursor: null } });
        },
      },
    };
  });
  await page.goto(dashboardPath);
  await page.getByRole("tab", { name: /推薦/ }).click();
  await page.getByRole("button", { name: /Synthetic waiting job/ }).click();
  // The day's scoring budget is spent and automatic processing is off, so the
  // job says why it is waiting and the push is still offered.
  await expect(page.locator("#screen-current")).toContainText("自動評分已關閉");
  await page.getByRole("button", { name: "立即處理這筆職缺" }).click();
  await expect(page.getByRole("button", { name: "立即處理這筆職缺" })).toHaveCount(0);
  await expect(page.locator("#screen-current")).toContainText("正在評分");

  await page.getByRole("tab", { name: "系統" }).click();
  await expect(page.locator("#screen-system")).toContainText("已停止");
  await page.getByRole("button", { name: "開啟自動篩選與評分" }).click();
  await expect(page.getByRole("button", { name: "關閉自動篩選與評分" })).toBeEnabled();
  const calls = await page.evaluate(() => window.__calls);
  expect(calls.filter((call) => call.path === "/api/v1/jobs/6/process" && call.method === "POST")).toHaveLength(1);
  expect(calls.some((call) => call.path === "/api/v1/settings" && call.method === "PUT" && call.body.auto_processing === true)).toBeTruthy();
});

test("options accepts a loopback endpoint and stores its token", async ({ page }) => {
  const saved = {};
  await page.addInitScript(() => {
    window.chrome = { storage: { local: {
      get: (defaults, callback) => callback(defaults),
      set: (values, callback) => { window.__savedOptions = values; callback(); },
    } } };
  });
  await page.goto(optionsPath);
  await page.locator("#endpoint").fill("http://127.0.0.1:18786");
  await page.locator("#token").fill("synthetic-token");
  await page.getByRole("button", { name: "儲存" }).click();
  await expect(page.getByRole("status")).toHaveText("已儲存");
  Object.assign(saved, await page.evaluate(() => window.__savedOptions));
  expect(saved).toEqual({ endpoint: "http://127.0.0.1:18786", token: "synthetic-token" });
});

test("recommendations append the next cursor page without duplicate jobs", async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 900 });
  await page.addInitScript(() => {
    const firstPage = Array.from({ length: 12 }, (_, index) => ({ id: index + 1, source: "yourator", title: `First synthetic job ${index + 1}`, company_name: "Example", score_total: 90 - index, process_state: "shortlisted", apply_state: "pending", verdict: "recommended" }));
    const second = { id: 99, source: "yourator", title: "Second synthetic job", company_name: "Example", score_total: 70, process_state: "shortlisted", apply_state: "pending", verdict: "recommended" };
    window.__calls = [];
    window.chrome = {
      storage: { local: {
        get: (defaults, callback) => callback(defaults),
        set: (_values, callback) => callback?.(),
      } },
      runtime: {
        onMessage: { addListener: () => {} },
        sendMessage: (request) => {
          window.__calls.push(request);
          if (request.type === "current-page") return Promise.resolve({ ok: true, context: { kind: "unsupported", status: "unsupported" } });
          if (request.type === "profile-api") return Promise.resolve({ ok: true, data: { status: "missing" } });
          if (request.path === "/api/v1/jobs/99") return Promise.resolve({ ok: true, data: { ...second, description: "Synthetic full JD", score: { total: 70 }, status_events: [] } });
          if (request.path?.startsWith("/api/v1/jobs?") && request.path.includes("cursor=MjA")) return Promise.resolve({ ok: true, data: { items: [firstPage[0], second], next_cursor: null } });
          if (request.path?.startsWith("/api/v1/jobs?")) return Promise.resolve({ ok: true, data: { items: firstPage, next_cursor: "MjA" } });
          if (request.path === "/api/v1/queue" || request.path === "/api/v1/runs") return Promise.resolve({ ok: true, data: { items: [], next_cursor: null } });
          return Promise.resolve({ ok: false, error: "unexpected request" });
        },
      },
    };
  });
  await page.goto(dashboardPath);
  await page.getByRole("tab", { name: /推薦/ }).click();
  await expect(page.getByRole("button", { name: "載入更多" })).toBeVisible();
  await page.getByRole("button", { name: "載入更多" }).scrollIntoViewIfNeeded();
  const scrollBeforeLoad = await page.evaluate(() => window.scrollY);
  expect(scrollBeforeLoad).toBeGreaterThan(0);
  await page.getByRole("button", { name: "載入更多" }).click();
  await expect(page.locator("#jobs .job-row")).toHaveCount(13);
  await expect(page.getByRole("button", { name: /First synthetic job 1 / })).toHaveCount(1);
  await expect(page.getByRole("button", { name: /Second synthetic job/ })).toHaveCount(1);
  await expect(page.getByRole("button", { name: "載入更多" })).toHaveCount(0);
  const scrollAfterLoad = await page.evaluate(() => window.scrollY);
  expect(Math.abs(scrollAfterLoad - scrollBeforeLoad)).toBeLessThanOrEqual(2);
  await page.getByRole("button", { name: /Second synthetic job/ }).click();
  await page.getByRole("tab", { name: /推薦/ }).click();
  const scrollAfterReturn = await page.evaluate(() => window.scrollY);
  expect(Math.abs(scrollAfterReturn - scrollAfterLoad)).toBeLessThanOrEqual(2);
  await expect(page.getByRole("button", { name: /Second synthetic job/ })).toHaveClass(/is-viewed/);
  const calls = await page.evaluate(() => window.__calls);
  expect(calls.some((call) => call.path?.includes("verdict=recommended") && call.path?.includes("cursor=MjA"))).toBeTruthy();
});

test("the queue appends the next cursor page without duplicate jobs", async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 900 });
  await page.addInitScript(() => {
    const firstPage = Array.from({ length: 12 }, (_, index) => ({ id: index + 1, source: "104", url: `https://www.104.com.tw/job/${index + 1}`, title: `First queued job ${index + 1}`, company_name: "Example", location: "Taipei" }));
    const second = { id: 99, source: "104", url: "https://www.104.com.tw/job/99", title: "Second queued job", company_name: "Example", location: "Taipei" };
    window.__calls = [];
    window.chrome = {
      storage: { local: {
        get: (defaults, callback) => callback(defaults),
        set: (_values, callback) => callback?.(),
      } },
      runtime: {
        onMessage: { addListener: () => {} },
        sendMessage: (request) => {
          window.__calls.push(request);
          if (request.type === "current-page") return Promise.resolve({ ok: true, context: { kind: "unsupported", status: "unsupported" } });
          if (request.type === "profile-api") return Promise.resolve({ ok: true, data: { status: "missing" } });
          if (request.path?.startsWith("/api/v1/queue?") && request.path.includes("cursor=MjA")) return Promise.resolve({ ok: true, data: { items: [firstPage[0], second], next_cursor: null } });
          if (request.path?.startsWith("/api/v1/queue")) return Promise.resolve({ ok: true, data: { items: firstPage, next_cursor: "MjA" } });
          if (request.path?.startsWith("/api/v1/jobs?") || request.path === "/api/v1/runs") return Promise.resolve({ ok: true, data: { items: [], next_cursor: null } });
          return Promise.resolve({ ok: false, error: "unexpected request" });
        },
      },
    };
  });
  await page.goto(dashboardPath);
  await page.getByRole("tab", { name: /待看/ }).click();
  await expect(page.getByRole("button", { name: "載入更多" })).toBeVisible();
  await page.getByRole("button", { name: "載入更多" }).scrollIntoViewIfNeeded();
  const scrollBeforeLoad = await page.evaluate(() => window.scrollY);
  expect(scrollBeforeLoad).toBeGreaterThan(0);
  await page.getByRole("button", { name: "載入更多" }).click();
  await expect(page.locator("#queue .job-row")).toHaveCount(13);
  await expect(page.getByRole("link", { name: /First queued job 1 / })).toHaveCount(1);
  await expect(page.getByRole("link", { name: /Second queued job/ })).toHaveCount(1);
  await expect(page.getByRole("button", { name: "載入更多" })).toHaveCount(0);
  const scrollAfterLoad = await page.evaluate(() => window.scrollY);
  expect(Math.abs(scrollAfterLoad - scrollBeforeLoad)).toBeLessThanOrEqual(2);
  await page.getByRole("tab", { name: /系統/ }).click();
  await page.getByRole("tab", { name: /待看/ }).click();
  const scrollAfterReturn = await page.evaluate(() => window.scrollY);
  expect(Math.abs(scrollAfterReturn - scrollAfterLoad)).toBeLessThanOrEqual(2);
  const calls = await page.evaluate(() => window.__calls);
  expect(calls.some((call) => call.path?.includes("/api/v1/queue?cursor=MjA"))).toBeTruthy();
});

test("service worker forwards API requests and reads current tab context", async () => {
  const manifest = JSON.parse(fs.readFileSync(path.join(__dirname, "../../../extension/manifest.json"), "utf8"));
  expect(manifest.side_panel.default_path).toBe("dashboard/index.html");
  expect(manifest.minimum_chrome_version).toBe("114");
  expect(manifest.action.default_popup).toBeUndefined();
  let listener;
  let request;
  const context = {
	URL,
    chrome: {
	  runtime: { onMessage: { addListener: (value) => { listener = value; } }, getURL: (value = "") => `chrome-extension://test-id/${value}` },
      storage: { local: { get: (_defaults, callback) => callback({ endpoint: "http://127.0.0.1:18786", token: "synthetic-token" }) } },
      tabs: { query: (_query, callback) => callback([{ id: 9 }]), sendMessage: async () => ({ kind: "job", status: "captured", job_id: 2 }) },
    },
	fetch: async (url, options) => { request = { url, options }; return { ok: true, headers: { get: (name) => name === "ETag" ? '"sha256:new"' : null }, json: async () => ({ items: [] }) }; },
  };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, "../../../extension/service-worker.js"), "utf8"), context);
  const response = await new Promise((resolve) => listener({ type: "api", path: "/api/v1/jobs", method: "GET" }, null, resolve));
  expect(response).toEqual({ ok: true, data: { items: [] } });
  expect(request).toEqual({ url: "http://127.0.0.1:18786/api/v1/jobs", options: { method: "GET", headers: { Authorization: "Bearer synthetic-token", "Content-Type": "application/json" }, body: undefined } });
	const profileResponse = await new Promise((resolve) => listener({ type: "profile-api", path: "/api/v1/profile", method: "PUT", etag: '"sha256:old"', body: { intents: { salary_target: 120000 } } }, { url: "chrome-extension://test-id/profile/index.html" }, resolve));
	expect(profileResponse).toEqual({ ok: true, data: { items: [] }, etag: '"sha256:new"' });
	expect(request.options.headers["If-Match"]).toBe('"sha256:old"');
	const reprocessResponse = await new Promise((resolve) => listener({ type: "profile-api", path: "/api/v1/profile/reprocess", method: "POST" }, { url: "chrome-extension://test-id/dashboard/index.html" }, resolve));
	expect(reprocessResponse.ok).toBe(true);
	const denied = await new Promise((resolve) => listener({ type: "api", path: "/api/v1/profile", method: "GET" }, { url: "https://www.104.com.tw/jobs" }, resolve));
	expect(denied.status).toBe(403);
	const deniedReprocess = await new Promise((resolve) => listener({ type: "api", path: "/api/v1/profile/reprocess", method: "POST" }, { url: "https://www.104.com.tw/jobs" }, resolve));
	expect(deniedReprocess.status).toBe(403);
  const current = await new Promise((resolve) => listener({ type: "current-page" }, null, resolve));
  expect(current).toEqual({ ok: true, context: { kind: "job", status: "captured", job_id: 2 } });
});
