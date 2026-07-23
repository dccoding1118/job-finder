const { test, expect } = require("@playwright/test");
const path = require("path");

const profilePath = `file://${path.join(__dirname, "../../../extension/profile/index.html")}`;

const profile = {
  summary: "Anonymous platform engineer",
  years_of_experience: 8,
  education: { degree: "master", field: "computer science" },
  experiences: [{ role: "backend engineer", org_type: "technology provider", years: 4, summary: "service delivery", achievements: ["reliable delivery"], skills: ["Go"] }],
  skills: { expert: ["Java"], proficient: ["Go"], familiar: ["Kubernetes"] },
  certifications: [],
  preferences: {
    salary_min: 0, salary_target: 0, locations: ["Taipei"], remote: "preferred",
    directions: [{ key: "P1", title: "cloud architecture", keywords: ["cloud"] }],
    industry_avoid: [],
    screening: { exclude_title_keywords: [], exclude_description_keywords: [], require_any_keywords: [], exclude_companies: [] },
  },
  honesty_bounds: ["configuration focused"],
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
        if (request.method === "GET") return { ok: true, etag: '"sha256:file"', data: { status: "ready", profile: fixture, profile_revision: `sha256:${"a".repeat(64)}`, issues: [], reprocess_estimate: { partial_screened: 2, requeued: 3, protected: 1, unchanged: 0 } } };
        return { ok: true, etag: '"sha256:new-file"', data: { status: "ready", profile_revision: `sha256:${"b".repeat(64)}`, semantic_changed: true } };
      } },
    };
  }, profile);

  await page.goto(profilePath);
  await expect(page.getByRole("heading", { name: "履歷與求職條件" })).toBeVisible();
  await expect(page.locator('[data-path="summary"]')).toHaveValue(profile.summary);
  expect((await page.evaluate(() => window.__profileCalls)).filter(({ method }) => method === "PUT")).toHaveLength(0);

  await page.locator('[data-path="summary"]').fill("Updated anonymous platform engineer");
  await expect(page.locator("#save-state")).toHaveText("尚未儲存");
  await page.getByRole("button", { name: "儲存 Profile" }).click();
  await expect(page.locator("#confirm-dialog")).toBeVisible();
  await expect(page.locator("#confirm-dialog")).toContainText("既有職缺保留原評分");
  await page.getByRole("button", { name: "確認儲存" }).click();
  await expect(page.locator("#message")).toContainText("Profile 已儲存");

  const put = (await page.evaluate(() => window.__profileCalls)).find(({ method }) => method === "PUT");
  expect(put.etag).toBe('"sha256:file"');
  expect(put.body.summary).toBe("Updated anonymous platform engineer");
	expect(await page.evaluate(() => window.__storageWrites.every((value) => Object.keys(value).every((key) => key === "theme")))).toBeTruthy();
});

test("Profile editor focuses the item added in the clicked section", async ({ page }) => {
  await page.addInitScript((fixture) => {
    window.chrome = {
      storage: { local: { get: (defaults, callback) => callback(defaults), set: (_values, callback) => callback?.() } },
      runtime: { sendMessage: async () => ({ ok: true, etag: '"sha256:file"', data: { status: "ready", profile: fixture, profile_revision: `sha256:${"a".repeat(64)}`, issues: [], reprocess_estimate: {} } }) },
    };
  }, { ...profile, experiences: [...profile.experiences, { ...profile.experiences[0], role: "second role", achievements: ["second achievement"] }] });

  await page.goto(profilePath);
  await page.locator('#experiences .item-card[data-index="0"] [data-add-nested="achievements"]').click();
  await expect(page.locator('[data-path="experiences.0.achievements.1"]')).toBeFocused();
});
