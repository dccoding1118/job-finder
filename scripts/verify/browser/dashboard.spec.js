const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const dashboardPath = `file://${path.join(__dirname, "../../../extension/dashboard/index.html")}`;
const optionsPath = `file://${path.join(__dirname, "../../../extension/options/index.html")}`;

test("Side Panel loads, filters, shows a job, persists theme, updates apply state, and starts a run", async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 900 });
  await page.addInitScript(() => {
    const jobs = [{ id: 2, source: "yourator", url: "https://www.yourator.co/jobs/2", title: "Synthetic job", company_name: "Example", salary_min: 100000, salary_max: 120000, location: "Taipei", score_total: 90.456, score_stale: true, score_profile_revision: `sha256:${"9".repeat(64)}`, evaluation_profile_revision: `sha256:${"9".repeat(64)}`, process_state: "letter_ready", apply_state: "pending", verdict: "recommended", letter_state: "ready" }];
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
		  if (request.type === "profile-api") return Promise.resolve({ ok: true, etag: '"sha256:file"', data: { status: "ready", profile_revision: `sha256:${"a".repeat(64)}`, summary: { years_of_experience: 8, skill_count: 3, experience_count: 1, directions: ["cloud architecture"] }, issues: [], reprocess_estimate: { requeued: 1, protected: 1 } } });
          if (request.type === "current-page") return Promise.resolve({ ok: true, context: { kind: "unsupported", status: "unsupported" } });
          if (request.path.startsWith("/api/v1/jobs?")) return Promise.resolve({ ok: true, data: { items: jobs } });
          if (request.path === "/api/v1/jobs/2") return Promise.resolve({ ok: true, data: { ...jobs[0], description: "Synthetic description", score: { hard_skill: 90, domain: 90, seniority: 90, condition: 90, direction: 90, total: 90, reason: "Synthetic" }, letter: { status: "approved", content: "Synthetic letter" }, status_events: [] } });
          if (request.path === "/api/v1/queue") return Promise.resolve({ ok: true, data: { items: [] } });
          if (request.path === "/api/v1/runs" && request.method === "POST") return Promise.resolve({ ok: true, data: { status: "started" } });
          if (request.path === "/api/v1/runs") return Promise.resolve({ ok: true, data: { items: [{ started_at: "2026-07-16T00:00:00+08:00", finished_at: null, trigger: "manual-extension", stats: { errors: 0, fetched: 4, new: 0, queries: 3 }, verdicts: { recommended: 2, not_recommended: 1, unfit: 1 }, error: null }] } });
          if (request.path === "/api/v1/jobs/2/apply") return Promise.resolve({ ok: true, data: { ...jobs[0], apply_state: request.body.apply_state, description: "Synthetic description", score: { hard_skill: 90, domain: 90, seniority: 90, condition: 90, direction: 90, total: 90, reason: "Synthetic" }, letter: { status: "approved", content: "Synthetic letter" }, status_events: [] } });
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
  await expect(page.locator("#score")).toContainText("技能90");
  await expect(page.locator(".score-total strong")).toHaveText("90");
  await expect(page.locator("#screen-current .revision-badge.is-stale")).toContainText("待重評");
  await page.locator("#theme-toggle").click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  expect(await page.evaluate(() => window.__storage.theme)).toBe("dark");
  await page.getByRole("button", { name: "更新投遞狀態" }).click();
  await page.getByRole("tab", { name: /推薦/ }).click();
  await page.getByText("進階篩選").click();
  await page.locator("#source-filter").selectOption("yourator");
  await page.locator("#process").selectOption("letter_ready");
  await page.getByRole("tab", { name: "系統" }).click();
	await expect(page.locator(".profile-card")).toContainText("年資 8 年");
	await expect(page.getByRole("button", { name: "編輯履歷與求職條件" })).toBeEnabled();
  await expect(page.getByText("每日 08:30（Asia/Taipei）")).toBeVisible();
  await page.getByRole("button", { name: "開啟連線設定" }).click();
  expect(await page.evaluate(() => window.__openedOptions)).toBeTruthy();
  await page.getByRole("button", { name: "更新過時評分職缺" }).click();
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
	const profileResponse = await new Promise((resolve) => listener({ type: "profile-api", path: "/api/v1/profile", method: "PUT", etag: '"sha256:old"', body: { summary: "synthetic" } }, { url: "chrome-extension://test-id/profile/index.html" }, resolve));
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
