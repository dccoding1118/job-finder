const fs = require("fs");
const path = require("path");
const { chromium } = require("playwright");

const root = process.env.VERIFY_ROOT;
const extension = process.env.VERIFY_EXTENSION_DIR;
const profile = process.env.VERIFY_BROWSER_PROFILE;
const evidenceRoot = process.env.VERIFY_EVIDENCE_ROOT;
const id = "oddnhajjhmgogefocnljofeahniodiei";
if (!root || !extension || !profile || !evidenceRoot) throw new Error("live verification paths are required");

async function main() {
  fs.rmSync(profile, { recursive: true, force: true });
  const context = await chromium.launchPersistentContext(profile, {
    headless: false,
    args: [`--disable-extensions-except=${extension}`, `--load-extension=${extension}`],
  });
  context.setDefaultTimeout(10_000);
  const result = {
    extension_id: id,
    connected: false,
    jobs_visible: false,
    official_source_visible: false,
    options_token_cleared: false,
    mutations_requested: false,
  };
  context.on("request", (request) => {
    if (!request.url().startsWith("http://127.0.0.1:18796/")) return;
    if (!["GET", "OPTIONS"].includes(request.method())) result.mutations_requested = true;
  });
  try {
    const options = await context.newPage();
    await options.goto(`chrome-extension://${id}/options/index.html`);
    await options.locator("#endpoint").fill("http://127.0.0.1:18796");
    await options.locator("#token").fill("verify-live-token");
    await options.getByRole("button", { name: "儲存" }).click();
    await options.getByRole("status").filter({ hasText: "已儲存" }).waitFor();
    result.options_token_cleared = await options.locator("#token").inputValue() === "";

    const dashboard = await context.newPage();
    await dashboard.goto(`chrome-extension://${id}/dashboard/index.html`);
    await dashboard.getByRole("status").filter({ hasText: "已連線至 jobfinder API" }).waitFor();
    result.connected = true;
    const firstJob = dashboard.locator("#jobs button").first();
    await firstJob.waitFor();
    result.jobs_visible = (await dashboard.locator("#jobs button").count()) > 0;
    await firstJob.click();
    const sourceURL = await dashboard.locator("#source").getAttribute("href");
    result.official_source_visible = Boolean(sourceURL && new URL(sourceURL).hostname === "www.yourator.co");
  } finally {
    const evidence = path.join(evidenceRoot, "extension-live-browser.json");
    fs.writeFileSync(evidence, `${JSON.stringify(result, null, 2)}\n`, { mode: 0o600 });
    await context.close();
  }
}

main().catch((err) => { console.error(err); process.exitCode = 1; });
