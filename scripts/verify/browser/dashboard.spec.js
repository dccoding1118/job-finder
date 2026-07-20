const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const dashboardPath = `file://${path.join(__dirname, "../../../extension/dashboard/index.html")}`;
const optionsPath = `file://${path.join(__dirname, "../../../extension/options/index.html")}`;

test("dashboard loads, filters, shows a job, updates apply state, and starts a run", async ({ page }) => {
  await page.addInitScript(() => {
    const jobs = [{ id: 2, source: "yourator", title: "Synthetic job", company_name: "Example", salary_min: 100000, salary_max: 120000, location: "Taipei", score_total: 90, process_state: "letter_ready", apply_state: "pending" }];
    window.__calls = [];
    window.chrome = { runtime: { sendMessage: (request) => {
      window.__calls.push(request);
      if (request.path.startsWith("/api/v1/jobs?")) return Promise.resolve({ ok: true, data: { items: jobs } });
      if (request.path === "/api/v1/jobs/2") return Promise.resolve({ ok: true, data: { ...jobs[0], description: "Synthetic description", score: { hard_skill: 90, domain: 90, seniority: 90, condition: 90, direction: 90, total: 90, reason: "Synthetic" }, letter: { status: "approved", content: "Synthetic letter" }, status_events: [] } });
      if (request.path === "/api/v1/queue") return Promise.resolve({ ok: true, data: { items: [] } });
      if (request.path === "/api/v1/runs" && request.method === "POST") return Promise.resolve({ ok: true, data: { status: "started" } });
      if (request.path === "/api/v1/runs") return Promise.resolve({ ok: true, data: { items: [{ started_at: "2026-07-16T00:00:00+08:00", finished_at: null, trigger: "manual-extension", stats: { errors: 0, fetched: 4, new: 0, queries: 3 }, verdicts: { recommended: 2, not_recommended: 1, unfit: 1 }, error: null }] } });
      return Promise.resolve({ ok: true, data: jobs[0] });
    } } };
    navigator.clipboard = { writeText: () => Promise.resolve() };
  });
  await page.goto(dashboardPath);
  await expect(page.getByRole("status")).toHaveText("已連線至 jobfinder API");
  await page.getByRole("button", { name: /Synthetic job/ }).click();
  await expect(page.locator("#letter")).toHaveText("Synthetic letter");
  await expect(page.locator("#score")).toContainText("總分 90｜技能 90｜領域 90｜資歷 90｜條件 90｜方向 90");
  await expect(page.locator("#runs")).toContainText("fetched=4");
  await expect(page.locator("#runs")).toContainText("recommended=2");
  await expect(page.locator("#runs")).not.toContainText("[object Object]");
  await page.locator("#source-filter").selectOption("yourator");
  await page.getByRole("button", { name: "更新狀態" }).click();
  await page.getByRole("button", { name: "手動抓取" }).click();
  await expect(page.getByRole("status")).toHaveText("已連線至 jobfinder API");
  const calls = await page.evaluate(() => window.__calls);
  expect(calls.some((call) => call.path === "/api/v1/jobs/2/apply" && call.body.apply_state === "pending")).toBeTruthy();
  expect(calls.some((call) => call.path === "/api/v1/runs" && call.method === "POST")).toBeTruthy();
  expect(calls.some((call) => call.path.includes("source=yourator"))).toBeTruthy();
});

test("options accepts a loopback endpoint and stores its token", async ({ page }) => {
  const saved = {};
  await page.addInitScript(() => {
    window.chrome = {
      storage: {
        local: {
          get: (defaults, callback) => callback(defaults),
          set: (values, callback) => { window.__savedOptions = values; callback(); },
        },
      },
    };
  });
  await page.goto(optionsPath);
  await page.locator("#endpoint").fill("http://127.0.0.1:18786");
  await page.locator("#token").fill("synthetic-token");
  await page.getByRole("button", { name: "儲存" }).click();
  await expect(page.getByRole("status")).toHaveText("已儲存");
  Object.assign(saved, await page.evaluate(() => window.__savedOptions));
  expect(saved).toEqual({ endpoint: "http://127.0.0.1:18786", token: "synthetic-token" });
});

test("service worker forwards an authenticated API request", async () => {
  let listener;
  let request;
  const context = {
    chrome: { runtime: { onMessage: { addListener: (value) => { listener = value; } } }, storage: { local: { get: (_defaults, callback) => callback({ endpoint: "http://127.0.0.1:18786", token: "synthetic-token" }) } } },
    fetch: async (url, options) => { request = { url, options }; return { ok: true, json: async () => ({ items: [] }) }; },
  };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, "../../../extension/service-worker.js"), "utf8"), context);
  const response = await new Promise((resolve) => listener({ type: "api", path: "/api/v1/jobs", method: "GET" }, null, resolve));
  expect(response).toEqual({ ok: true, data: { items: [] } });
  expect(request).toEqual({ url: "http://127.0.0.1:18786/api/v1/jobs", options: { method: "GET", headers: { Authorization: "Bearer synthetic-token", "Content-Type": "application/json" }, body: undefined } });
});
