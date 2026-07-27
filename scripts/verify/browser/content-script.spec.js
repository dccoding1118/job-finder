const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");

// Content-script E2E: loads the 104 search / notification / detail fixtures on
// their real host, mocks the local API through chrome.runtime.sendMessage, and
// asserts the marks and current-page context the content scripts expose. This is the regression
// guard for the live-DOM selectors — the title attribute over the highlight
// spans, the data-gtm anchor nested inside .info-tags__text, and the Vue-injected
// JobPosting JSON-LD — so a selector drift fails CI instead of only live.

function script(name) {
  return fs.readFileSync(path.join(__dirname, "../../../extension/content", name), "utf8");
}

// mark.js carries the shared verdict badge and is injected first, exactly as the
// manifest declares it for both platforms.
const markScript = script("mark.js");
const listScript = script("list.js");
const jobScript = script("job.js");
// Cake is one injection covering the whole site: the mode modules register
// themselves and the router picks one from the URL, exactly as the manifest
// declares the order.
const cakeScripts = ["mark.js", "nav.js", "cake-list.js", "cake-job.js", "cake.js"].map(script);

async function loadCake(page) {
  for (const source of cakeScripts) {
    await page.addScriptTag({ content: source });
  }
}

function fixture(name) {
  return fs.readFileSync(path.join(__dirname, "fixtures/104", name), "utf8");
}

function cakeFixture(name) {
  return fs.readFileSync(path.join(__dirname, "fixtures/cake", name), "utf8");
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
  await page.addScriptTag({ content: markScript });
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
  await page.addScriptTag({ content: markScript });
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

// ET-40: the embedded state is preferred while it still matches the conditions on
// screen, and its fields are normalized into the same capture shape 104 sends.
test("cake list captures the embedded state and marks each item", async ({ page }) => {
  await installAPI(page, {
    "/api/v1/capture/list": {
      ok: true,
      data: {
        items: [
          { external_id: "example-cloud/senior-platform-engineer", verdict: "recommended", score_total: 88 },
          { external_id: "example-cloud/platform-intern", verdict: "unfit", filter_hits: ["exclude_title_keywords"] },
        ],
      },
    },
  });
  await serveFixture(page, "https://www.cake.me/**", cakeFixture("search.html"));
  await page.goto("https://www.cake.me/jobs?query=platform&page=1");
  await loadCake(page);

  await expect(page.locator(".jobfinder-mark")).toHaveCount(2);
  const badges = (await page.locator(".jobfinder-mark .badge").allInnerTexts()).join(" | ");
  expect(badges).toContain("推薦");
  expect(badges).toContain("總分 88");
  expect(badges).toContain("命中 exclude_title_keywords");

  const capture = await page.evaluate(() => window.__calls.find((c) => c.path === "/api/v1/capture/list"));
  expect(capture.body.source).toBe("cake");
  expect(capture.body.next_data).toContain("entityByPathId");
  expect(capture.body.items).toBeUndefined();

  const context = await page.evaluate(() => new Promise((resolve) => window.__contentListener({ type: "get-page-context" }, {}, resolve)));
  expect(context).toMatchObject({ kind: "list", source: "cake" });
});

// ET-41 / ET-42: once the user turns a page the embedded state is stale, so the
// DOM is harvested instead — through the hashed class names, which the selectors
// must not depend on.
test("cake list falls back to the DOM when the embedded state is stale", async ({ page }) => {
  await installAPI(page, {
    "/api/v1/capture/list": {
      ok: true,
      data: { items: [{ external_id: "example-cloud/senior-platform-engineer", verdict: "pending_detail" }] },
    },
  });
  await serveFixture(page, "https://www.cake.me/**", cakeFixture("search.html"));
  await page.goto("https://www.cake.me/jobs?query=platform&page=2");
  await loadCake(page);

  await expect.poll(() => page.evaluate(() => window.__calls.some((call) => call.path === "/api/v1/capture/list"))).toBe(true);
  const capture = await page.evaluate(() => window.__calls.find((c) => c.path === "/api/v1/capture/list"));
  expect(capture.body.next_data).toBeUndefined();
  expect(capture.body.items).toHaveLength(2);
  const first = capture.body.items.find((item) => item.href.includes("senior-platform-engineer"));
  expect(first.title).toBe("資深雲端平台工程師");
  expect(first.company_name).toBe("合成雲端股份有限公司");
  expect(first.location).toBe("台北市");
  expect(first.salary_text).toBe("月薪 80,000 ~ 120,000 元");
  await expect(page.locator(".jobfinder-mark")).toHaveCount(1);
});

// Cake links the same job from blocks outside the result list (recommendations,
// recently viewed) — which is where an already-assessed job shows up. Every
// occurrence is marked, and the capture fields still come from the result entry.
test("cake list marks every occurrence of a job, not only the first link on the page", async ({ page }) => {
  await installAPI(page, {
    "/api/v1/capture/list": {
      ok: true,
      data: { items: [{ external_id: "example-cloud/senior-platform-engineer", verdict: "recommended", score_total: 88 }] },
    },
  });
  // The extra link sits before the result list and has no JobSearchItem ancestor,
  // exactly like Cake's own recommendation blocks.
  const withAside = cakeFixture("search.html").replace(
    '<main id="__next">',
    '<main id="__next"><aside id="recent"><a href="/companies/example-cloud/jobs/senior-platform-engineer">資深雲端平台工程師</a></aside>',
  );
  await serveFixture(page, "https://www.cake.me/**", withAside);
  await page.goto("https://www.cake.me/jobs?query=platform&page=2");
  await loadCake(page);

  // Both the aside and the result entry carry the mark.
  await expect(page.locator("#recent .jobfinder-mark")).toHaveCount(1);
  await expect(page.locator('[class*="JobSearchItem"] .jobfinder-mark')).toHaveCount(1);

  // The fields were read from the result entry, not from the bare aside link.
  const capture = await page.evaluate(() => window.__calls.find((c) => c.path === "/api/v1/capture/list"));
  const item = capture.body.items.find((entry) => entry.href.includes("senior-platform-engineer"));
  expect(item.company_name).toBe("合成雲端股份有限公司");
  expect(item.location).toBe("台北市");
});

// An item the first capture answered nothing about must not stay unmarked until
// the user reloads: the entry is asked about again on its own.
test("cake list asks again for an item the first capture left out", async ({ page }) => {
  await page.addInitScript(() => {
    window.__calls = [];
    let round = 0;
    window.chrome = {
      runtime: {
        onMessage: { addListener: (listener) => { window.__contentListener = listener; } },
        sendMessage: (request) => {
          window.__calls.push(request);
          if (request.path !== "/api/v1/capture/list") return Promise.resolve({ ok: true, data: {} });
          round += 1;
          // The first answer covers one of the two visible entries only.
          const items = [{ external_id: "example-cloud/senior-platform-engineer", verdict: "pending_detail" }];
          if (round > 1) items.push({ external_id: "example-cloud/platform-intern", verdict: "pending_score" });
          return Promise.resolve({ ok: true, data: { items } });
        },
      },
    };
  });
  await serveFixture(page, "https://www.cake.me/**", cakeFixture("search.html"));
  await page.goto("https://www.cake.me/jobs?query=platform&page=2");
  await loadCake(page);

  await expect(page.locator(".jobfinder-mark")).toHaveCount(2);
  expect((await page.locator(".jobfinder-mark .badge").allInnerTexts()).join(" | ")).toContain("評分中");
});

// ET-43: the detail page is read off the rendered DOM — through the hashed class
// names, which the selectors must not depend on — and reports the same context
// the 104 path does, so the Side Panel presents both identically.
test("cake detail page harvests the rendered JD and exposes the shared context", async ({ page }) => {
  await installAPI(page, {
    "/api/v1/capture/job": { ok: true, data: { id: 9, verdict: "pending_score" } },
  });
  await serveFixture(page, "https://www.cake.me/**", cakeFixture("job.html"));
  await page.goto("https://www.cake.me/companies/example-cloud/jobs/senior-platform-engineer");
  await loadCake(page);

  await expect(page.locator("#jobfinder-sidebar")).toHaveCount(0);
  await expect.poll(() => page.evaluate(() => window.__calls.some((call) => call.path === "/api/v1/capture/job"))).toBe(true);
  const context = await page.evaluate(() => new Promise((resolve) => window.__contentListener({ type: "get-page-context" }, {}, resolve)));
  expect(context).toMatchObject({ kind: "job", source: "cake", status: "captured", job_id: 9, verdict: "pending_score" });

  const capture = await page.evaluate(() => window.__calls.find((c) => c.path === "/api/v1/capture/job"));
  expect(capture.body.source).toBe("cake");
  expect(capture.body.url).toContain("/companies/example-cloud/jobs/senior-platform-engineer");
  // The embedded state is never read on a detail page: it describes the list the
  // user came from.
  expect(capture.body.next_data).toBeUndefined();
  expect(capture.body.cake_dom.title).toBe("資深雲端平台工程師");
  expect(capture.body.cake_dom.company_name).toBe("合成雲端股份有限公司");
  expect(capture.body.cake_dom.sections.map((section) => section.title)).toEqual(["職缺描述", "職務需求"]);
  expect(capture.body.cake_dom.sections[1].body).toContain("熟悉 Go 平台開發");
  expect(capture.body.cake_dom.meta).toContain("台北市內湖區");
  expect(capture.body.cake_dom.meta).toContain("月薪 80,000 ~ 120,000 元");
});

// ET-44: the user reaches a JD by clicking a list entry, which Cake serves
// without loading a document. The mode has to follow that navigation, because
// Chrome injects a content script once per document and would otherwise leave the
// list mode answering for a detail page.
test("cake follows a client-side navigation from the list to a detail page", async ({ page }) => {
  await installAPI(page, {
    "/api/v1/capture/list": { ok: true, data: { items: [] } },
    "/api/v1/capture/job": { ok: true, data: { id: 12, verdict: "pending_score" } },
  });
  await page.route("https://www.cake.me/companies/**", (route) => route.fulfill({ contentType: "text/html; charset=utf-8", body: cakeFixture("job.html") }));
  await serveFixture(page, "https://www.cake.me/jobs**", cakeFixture("search.html"));
  await page.goto("https://www.cake.me/jobs?query=platform&page=1");
  await loadCake(page);

  const listContext = await page.evaluate(() => new Promise((resolve) => window.__contentListener({ type: "get-page-context" }, {}, resolve)));
  expect(listContext).toMatchObject({ kind: "list", source: "cake" });

  // Cake replaces the body and pushes the JD's URL, which is all a soft
  // navigation is.
  await page.evaluate(async () => {
    const response = await fetch("https://www.cake.me/companies/example-cloud/jobs/senior-platform-engineer");
    const html = await response.text();
    document.body.innerHTML = new DOMParser().parseFromString(html, "text/html").body.innerHTML;
    history.pushState({}, "", "/companies/example-cloud/jobs/senior-platform-engineer");
  });

  await expect.poll(() => page.evaluate(() => window.__calls.some((call) => call.path === "/api/v1/capture/job"))).toBe(true);
  const context = await page.evaluate(() => new Promise((resolve) => window.__contentListener({ type: "get-page-context" }, {}, resolve)));
  expect(context).toMatchObject({ kind: "job", source: "cake", status: "captured", job_id: 12 });
  const capture = await page.evaluate(() => window.__calls.find((c) => c.path === "/api/v1/capture/job"));
  expect(capture.body.url).toContain("/companies/example-cloud/jobs/senior-platform-engineer");
  expect(capture.body.cake_dom.sections.length).toBeGreaterThan(0);
});
