const path = require("path");

// Playwright configuration for the isolated browser E2E suite.
/** @type {import('@playwright/test').PlaywrightTestConfig} */
module.exports = {
  testDir: ".",
  outputDir: path.join(__dirname, "../../../.local-dev/dev-verify/playwright-results"),
  timeout: 30_000,
  retries: 0,
  workers: 1,
  reporter: [["list"]],
  use: {
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
};
