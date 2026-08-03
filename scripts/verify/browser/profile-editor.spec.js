const { test, expect } = require("@playwright/test");
const path = require("path");

const profilePath = `file://${path.join(__dirname, "../../../extension/profile/index.html")}`;

const profile = {
  search: {
    directions: [{ key: "P1", title: "cloud architecture", keywords: ["cloud"] }],
  },
  requirements: {
    salary_min: 90000,
    locations: ["taipei"],
    remote: "acceptable",
    employment_types: [],
    industry_avoid: [],
    exclude_title_keywords: ["intern"],
    exclude_description_keywords: [],
    exclude_companies: [],
  },
  intents: {
    salary_target: 120000,
    content_likes: ["building reliable cloud services"],
    content_dislikes: ["manual release procedures"],
    industry_interests: ["developer tooling"],
  },
  experiences: [
    { industry: "technology services", years: 4, is_management: false, exclude_from_totals: false, skills: ["Go"], org_type: "technology provider", role: "backend engineer", achievements: ["reliable delivery"] },
  ],
  qualifications: {
    education: [{ level: "master", field: "computer science", status: "graduated" }],
    skills: [{ name: "Java", level: "expert" }],
    certifications: [],
    languages: [],
  },
  honesty_bounds: ["configuration focused"],
};

const readyResponse = {
  status: "ready",
  profile,
  filter_revision: `sha256:${"a".repeat(64)}`,
  score_revision: `sha256:${"c".repeat(64)}`,
  issues: [],
  reprocess_estimate: { partial_screened: 2, requeued: 3, protected: 1, unchanged: 0 },
};

test("Profile editor keeps its draft in memory and saves with the loaded ETag", async ({ page }) => {
  await page.addInitScript((fixture) => {
    window.__profileCalls = [];
    window.__storageWrites = [];
    window.chrome = {
      storage: { local: {
        get: (defaults, callback) => callback(defaults),
        set: (values, callback) => { window.__storageWrites.push(values); callback?.(); },
      } },
      runtime: { sendMessage: async (request) => {
        window.__profileCalls.push(request);
        if (request.method === "GET") return { ok: true, etag: '"sha256:file"', data: fixture };
        return { ok: true, etag: '"sha256:new-file"', data: { status: "ready", filter_revision: `sha256:${"b".repeat(64)}`, score_revision: `sha256:${"d".repeat(64)}`, filter_changed: true, score_changed: true } };
      } },
    };
  }, readyResponse);

  await page.goto(profilePath);
  await expect(page.getByRole("heading", { name: "求職條件與履歷" })).toBeVisible();
  await expect(page.locator('[data-path="requirements.salary_min"]')).toHaveValue(String(profile.requirements.salary_min));
  await expect(page.locator('[data-path="requirements.remote"]')).toHaveValue(profile.requirements.remote);
  expect((await page.evaluate(() => window.__profileCalls)).filter(({ method }) => method === "PUT")).toHaveLength(0);

  await page.locator('[data-path="requirements.salary_min"]').fill("95000");
  await expect(page.locator("#save-state")).toHaveText("尚未儲存");
  await page.getByRole("button", { name: "儲存 Profile" }).click();
  await expect(page.locator("#confirm-dialog")).toBeVisible();
  await expect(page.locator("#confirm-dialog")).toContainText("既有職缺保留原判定與版號");
  await page.getByRole("button", { name: "確認儲存" }).click();
  await expect(page.locator("#message")).toContainText("Profile 已儲存");

  const put = (await page.evaluate(() => window.__profileCalls)).find(({ method }) => method === "PUT");
  expect(put.etag).toBe('"sha256:file"');
  expect(put.body.requirements.salary_min).toBe(95000);
  expect(await page.evaluate(() => window.__storageWrites.every((value) => Object.keys(value).every((key) => key === "theme")))).toBeTruthy();
});

test("Profile editor focuses the item added in the clicked section", async ({ page }) => {
  await page.addInitScript((fixture) => {
    window.chrome = {
      storage: { local: { get: (defaults, callback) => callback(defaults), set: (_values, callback) => callback?.() } },
      runtime: { sendMessage: async () => ({ ok: true, etag: '"sha256:file"', data: fixture }) },
    };
  }, { ...readyResponse, profile: { ...profile, experiences: [...profile.experiences, { ...profile.experiences[0], role: "second role", achievements: ["second achievement"] }] }, reprocess_estimate: {} });

  await page.goto(profilePath);
  await page.locator('#experiences .item-card[data-index="0"] [data-add-nested="achievements"]').click();
  await expect(page.locator('[data-path="experiences.0.achievements.1"]')).toBeFocused();
});

test("Profile editor picks locations from the vocabulary and loads skills from experiences on request", async ({ page }) => {
  await page.addInitScript((fixture) => {
    window.chrome = {
      storage: { local: { get: (defaults, callback) => callback(defaults), set: (_values, callback) => callback?.() } },
      runtime: { sendMessage: async () => ({ ok: true, etag: '"sha256:file"', data: fixture }) },
    };
  }, readyResponse);

  await page.goto(profilePath);

  // A locality is a dropdown of canonical keys, and one already chosen is not
  // offered again on another row.
  const firstLocation = page.locator('[data-path="requirements.locations.0"]');
  await expect(firstLocation).toHaveValue("taipei");
  await page.locator('[data-add-list="requirements.locations"]').click();
  const secondLocation = page.locator('[data-path="requirements.locations.1"]');
  await secondLocation.selectOption("taiwan");
  await expect(secondLocation.locator('option[value="taipei"]')).toHaveCount(0);

  // 全台 is a shortcut, not a value: it fills in every locality in Taiwan and
  // leaves 海外 off the list.
  await page.getByRole("button", { name: "＋ 全台" }).click();
  await expect(page.locator("#requirement-locations .compact-row")).toHaveCount(23);
  const chosen = await page.locator("#requirement-locations select").evaluateAll((nodes) => nodes.map((node) => node.value));
  expect(chosen).not.toContain("overseas");
  expect(chosen).toContain("taiwan");

  // The experience skill is carried into the totals list only when asked, at
  // 專家, and the skill already listed keeps its own proficiency.
  await expect(page.locator("#skills .compact-row")).toHaveCount(1);
  await page.getByRole("button", { name: "從經歷載入" }).click();
  await expect(page.locator("#skills .compact-row")).toHaveCount(2);
  await expect(page.locator('[data-path="qualifications.skills.0.name"]')).toHaveValue("Java");
  await expect(page.locator('[data-path="qualifications.skills.0.level"]')).toHaveValue("expert");
  await expect(page.locator('[data-path="qualifications.skills.1.name"]')).toHaveValue("Go");
  await expect(page.locator('[data-path="qualifications.skills.1.level"]')).toHaveValue("expert");
  await page.locator('[data-path="qualifications.skills.1.level"]').selectOption("familiar");
  await page.getByRole("button", { name: "從經歷載入" }).click();
  await expect(page.locator("#skills .compact-row")).toHaveCount(2);
  await expect(page.locator('[data-path="qualifications.skills.1.level"]')).toHaveValue("familiar");
});
