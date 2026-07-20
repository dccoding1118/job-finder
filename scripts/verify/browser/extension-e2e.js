const fs = require("fs");
const path = require("path");
const { chromium } = require("playwright");

const root = process.env.VERIFY_ROOT;
const id = "oddnhajjhmgogefocnljofeahniodiei";
if (!root) throw new Error("VERIFY_ROOT is required");

async function main() {
  const extension = process.env.VERIFY_EXTENSION_DIR || path.join(root, "artifact", "extension");
  const profile = process.env.VERIFY_BROWSER_PROFILE || path.join(root, "runtime", "browser-profile-mock");
  const evidenceRoot = process.env.VERIFY_EVIDENCE_ROOT || path.join(root, "evidence");
  const evidence = path.join(evidenceRoot, "extension-browser.json");
  fs.rmSync(profile, { recursive: true, force: true });
  const context = await chromium.launchPersistentContext(profile, {
    headless: false,
    args: [`--disable-extensions-except=${extension}`, `--load-extension=${extension}`],
  });
  context.setDefaultTimeout(10_000);
  const observationTasks = [];
  const observations = [];
  const result = {
    extension_id: id,
    connected: false,
    options_token_cleared: false,
    filters_verified: false,
    detail_verified: false,
    copied: false,
    clipboard_matches: false,
    apply_requested: false,
    apply_state_after_response: null,
    generate_requested: false,
    generate_letter_state_after_response: null,
    manual_run_requested: false,
    run_history_rendered: false,
    screenshot: "extension-dashboard.png",
  };
  context.on("request", (request) => {
    if (!request.url().startsWith("http://127.0.0.1:18786/")) return;
    observationTasks.push((async () => {
      const headers = await request.allHeaders();
      const url = new URL(request.url());
      observations.push({
        method: request.method(),
        path: `${url.pathname}${url.search}`,
        kind: request.method() === "OPTIONS" ? "preflight" : "actual",
        origin: headers.origin || "absent",
        authorization_present: Boolean(headers.authorization),
      });
    })());
  });
  try {
    console.error(`extension_workers=${context.serviceWorkers().map((worker) => worker.url()).join(",")}`);
    const options = await context.newPage();
    await options.goto(`chrome-extension://${id}/options/index.html`);
    await options.locator("#endpoint").fill("http://127.0.0.1:18786");
    await options.locator("#token").fill("verify-only-token");
    await options.getByRole("button", { name: "儲存" }).click();
    await options.getByRole("status").filter({ hasText: "已儲存" }).waitFor();
    result.options_token_cleared = await options.locator("#token").inputValue() === "";

    const dashboard = await context.newPage();
    await dashboard.goto(`chrome-extension://${id}/dashboard/index.html`);
    try {
      await dashboard.getByRole("status").filter({ hasText: "已連線至 jobfinder API" }).waitFor();
    } catch (err) {
      console.error(`dashboard_status=${await dashboard.locator("#status").textContent()}`);
      throw err;
    }
    result.connected = true;
    const collection = await dashboard.evaluate(() => chrome.runtime.sendMessage({ type: "api", path: "/api/v1/jobs", method: "GET" }));
    const readyJob = collection.data.items.find((job) => job.title === "Verification ready hybrid backend engineer");
    const failedJob = collection.data.items.find((job) => job.title === "Verification failure remote platform engineer");
    if (!readyJob || !failedJob) throw new Error("verification fixture jobs are absent from the API");

    // The recommended verdict spans both letter outcomes; narrowing by source,
    // process and apply isolates the single approved job.
    await dashboard.locator("#verdict").selectOption("recommended");
    await dashboard.locator("#source-filter").selectOption("yourator");
    await dashboard.getByRole("button", { name: /Verification ready hybrid backend engineer/ }).waitFor();
    await dashboard.locator("#process").selectOption("letter_ready");
    await dashboard.locator("#apply").selectOption("pending");
    // Each filter change fires an un-awaited load(), and their renders can resolve
    // out of order, so poll until the list settles to exactly the single expected
    // job instead of sampling the count once mid-render.
    for (let attempt = 0; attempt < 50; attempt += 1) {
      const total = await dashboard.locator("#jobs button").count();
      const ready = await dashboard.getByRole("button", { name: /Verification ready hybrid backend engineer/ }).count();
      result.filters_verified = total === 1 && ready === 1;
      if (result.filters_verified) break;
      await dashboard.waitForTimeout(100);
    }

    await dashboard.getByRole("button", { name: /Verification ready hybrid backend engineer/ }).click();
    await dashboard.locator("#letter").waitFor();
    await dashboard.locator("#score").filter({ hasText: "總分 90｜技能 90｜領域 90｜資歷 90｜條件 90｜方向 90：合成核准情境" }).waitFor();
    if (!(await dashboard.locator("#verdict-label").textContent()).includes("推薦")) throw new Error("dashboard verdict label is invalid");
    if (await dashboard.locator("#description").textContent() !== "Build Go backend services") throw new Error("dashboard description is invalid");
    if (await dashboard.locator("#source").getAttribute("href") !== readyJob.url) throw new Error("dashboard source URL is invalid");
    result.detail_verified = true;

    await dashboard.getByRole("button", { name: "複製信件" }).click();
    await dashboard.getByRole("status").filter({ hasText: "已複製信件" }).waitFor();
    result.copied = true;
    const copied = await dashboard.evaluate(() => navigator.clipboard.readText());
    result.clipboard_matches = copied === "我使用 Go 建立可靠服務。[你的姓名][你的聯絡方式]";

    await dashboard.locator("#apply-state").selectOption("applied");
    await dashboard.getByRole("button", { name: "更新狀態" }).click();
    result.apply_requested = true;
    // load() resets the status line to the connected note right after the update,
    // so the persisted apply_state is confirmed from the API read-back rather than
    // the transient "投遞狀態已更新" status message.
    let applied;
    for (let attempt = 0; attempt < 50; attempt += 1) {
      applied = await dashboard.evaluate((jobId) => chrome.runtime.sendMessage({ type: "api", path: `/api/v1/jobs/${jobId}`, method: "GET" }), readyJob.id);
      if (applied.data?.apply_state === "applied") break;
      await dashboard.waitForTimeout(100);
    }
    result.apply_state_after_response = applied.data?.apply_state || null;

    // The generate entry: a letter_failed recommended job offers another attempt,
    // and the request is accepted (letter_requested) rather than awaited.
    await dashboard.locator("#apply").selectOption("");
    await dashboard.locator("#process").selectOption("letter_failed");
    await dashboard.getByRole("button", { name: /Verification failure remote platform engineer/ }).click();
    await dashboard.getByRole("button", { name: "再次產生求職信" }).click();
    // The accepted request is shown on #letter-state, which load() leaves intact,
    // rather than the status line, which load() overwrites with the connected note.
    await dashboard.waitForFunction(() => document.querySelector("#letter-state")?.textContent?.includes("產生中"));
    result.generate_requested = true;
    result.generate_letter_state_after_response = "requested";

    await dashboard.getByRole("button", { name: "手動抓取" }).click();
    result.manual_run_requested = true;
    // The "已開始抓取" acknowledgement is immediately overwritten by load()'s
    // connected note, so the durable proof is the manual-extension run appearing
    // in the history below rather than that transient status message.
    for (let attempt = 0; attempt < 50; attempt += 1) {
      await dashboard.locator("#refresh").click();
      const runText = await dashboard.locator("#runs").textContent();
      if (runText.includes("manual-extension") && runText.includes("fetched=") && !runText.includes("[object Object]")) {
        result.run_history_rendered = true;
        break;
      }
      await dashboard.waitForTimeout(100);
    }
    await dashboard.screenshot({ path: path.join(evidenceRoot, "extension-dashboard.png") });
  } finally {
    await Promise.all(observationTasks);
    fs.writeFileSync(evidence, `${JSON.stringify({ result, requests: observations }, null, 2)}\n`, { mode: 0o600 });
    await context.close();
  }
}

main().catch((err) => { console.error(err); process.exitCode = 1; });
