const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");

// Content-script E2E: loads the 104 search / notification / detail fixtures on
// their real host, mocks the local API through chrome.runtime.sendMessage, and
// asserts the marks and current-page context the content scripts expose. This is the regression
// guard for the live-DOM selectors — the title attribute over the highlight
// spans, the data-gtm anchor nested inside .info-tags__text, and the Vue-injected
// JobPosting JSON-LD — so a selector drift fails CI instead of only live.

const listScript = fs.readFileSync(path.join(__dirname, "../../../extension/content/list.js"), "utf8");
const jobScript = fs.readFileSync(path.join(__dirname, "../../../extension/content/job.js"), "utf8");

function fixture(name) {
  return fs.readFileSync(path.join(__dirname, "fixtures/104", name), "utf8");
}

// serveFixture answers a captured host with the fixture body so the page loads
// under its real origin (location.hostname drives which list layout is read).
async function serveFixture(page, hostGlob, body) {
  await page.route(hostGlob, (route) => route.fulfill({ contentType: "text/html; charset=utf-8", body }));
}

// installAPI records every content-script request and answers it from `answer`.
async function installAPI(page, answer) {
  await page.addInitScript((responses) => {
    window.__calls = [];
    window.chrome = {
      runtime: {
        onMessage: { addListener: (listener) => { window.__contentListener = listener; } },
        sendMessage: (request) => {
          window.__calls.push(request);
          const reply = responses[request.path];
          return Promise.resolve(reply || { ok: true, data: {} });
        },
      },
    };
  }, answer);
}

test("search page marks each real result and skips the ad, reading the live selectors", async ({ page }) => {
  await installAPI(page, {
    "/api/v1/capture/list": {
      ok: true,
      data: {
        items: [
          { external_id: "100001", verdict: "recommended", score_total: 88 },
          { external_id: "100002", verdict: "unfit", filter_hits: ["exclude_title_keywords"] },
          { external_id: "100003", verdict: "pending_detail" },
        ],
      },
    },
  });
  await serveFixture(page, "https://www.104.com.tw/**", fixture("search.html"));
  await page.goto("https://www.104.com.tw/jobs/search/?keyword=platform");
  await page.addScriptTag({ content: listScript });

  // Four articles, but the hotjob ad carries no mark; the three real results do.
  await expect(page.locator(".job-summary")).toHaveCount(4);
  await expect(page.locator(".jobfinder-mark")).toHaveCount(3);

  const badges = (await page.locator(".jobfinder-mark .badge").allInnerTexts()).join(" | ");
  expect(badges).toContain("推薦");
  expect(badges).toContain("總分 88");
  expect(badges).toContain("不適合");
  expect(badges).toContain("命中 exclude_title_keywords");
  expect(badges).toContain("待看");

  // The captured payload proves the selector fixes: the title attribute is read
  // whole over the highlight span, the location/salary come from the data-gtm
  // anchor nested in .info-tags__text, and the ad is excluded.
  const capture = await page.evaluate(() => window.__calls.find((c) => c.path === "/api/v1/capture/list"));
  const first = capture.body.items.find((item) => item.href.includes("/job/100001"));
  expect(first.title).toBe("資深雲端平台工程師 (Cloud Platform / SRE)");
  expect(first.location).toBe("台北市信義區");
  expect(first.salary_text).toBe("月薪70,000~95,000元");
  expect(first.remote).toBe(true);
  expect(capture.body.items.some((item) => item.href.includes("/job/ad9001"))).toBe(false);
});

test("notification page reads its unlabelled tags by position and format", async ({ page }) => {
  await installAPI(page, {
    "/api/v1/capture/list": {
      ok: true,
      data: {
        items: [
          { external_id: "200001", verdict: "recommended", score_total: 91 },
          { external_id: "200002", verdict: "unfit", filter_hits: ["exclude_title_keywords"] },
          { external_id: "200003", verdict: "pending_detail" },
        ],
      },
    },
  });
  await serveFixture(page, "https://pda.104.com.tw/**", fixture("notification.html"));
  await page.goto("https://pda.104.com.tw/work/mate/list/1");
  await page.addScriptTag({ content: listScript });

  await expect(page.locator(".jobfinder-mark")).toHaveCount(3);

  const capture = await page.evaluate(() => window.__calls.find((c) => c.path === "/api/v1/capture/list"));
  const first = capture.body.items.find((item) => item.href.includes("/job/200001"));
  expect(first.title).toBe("雲端架構師 (Cloud Architect)");
  // No data-gtm here: location is the first .info-tags__text, salary is found in
  // .info-othertags__text by format.
  expect(first.location).toBe("台北市大安區");
  expect(first.salary_text).toBe("月薪80,000~120,000元");
  const remote = capture.body.items.find((item) => item.href.includes("/job/200003"));
  expect(remote.remote).toBe(true);
});

test("detail page exposes captured Job context without injecting an overlay", async ({ page }) => {
  await installAPI(page, {
    "/api/v1/capture/job": {
      ok: true,
      data: {
        id: 5,
        verdict: "recommended",
        score: { total: 88, hard_skill: 80, domain: 90, seniority: 85, condition: 88, direction: 80, reason: "合成評分理由" },
      },
    },
  });
  await serveFixture(page, "https://www.104.com.tw/**", fixture("job.html"));
  await page.goto("https://www.104.com.tw/job/300001");
  await page.addScriptTag({ content: jobScript });

  await expect(page.locator("#jobfinder-sidebar")).toHaveCount(0);
  await expect.poll(() => page.evaluate(() => window.__calls.some((call) => call.type === "page-context-updated"))).toBe(true);
  const context = await page.evaluate(() => new Promise((resolve) => window.__contentListener({ type: "get-page-context" }, {}, resolve)));
  expect(context).toMatchObject({ kind: "job", source: "104", status: "captured", job_id: 5, verdict: "recommended" });

  // The capture payload carries the page's JobPosting JSON-LD, confirming the
  // script[type="application/ld+json"] read the Vue-injected block.
  const capture = await page.evaluate(() => window.__calls.find((c) => c.path === "/api/v1/capture/job"));
  expect(capture.body.url).toContain("/job/300001");
  expect(capture.body.json_ld.some((block) => block.includes("JobPosting"))).toBe(true);
});
