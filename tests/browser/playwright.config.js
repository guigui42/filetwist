import { defineConfig } from "@playwright/test";

const image = process.env.FILETWIST_TEST_IMAGE || "filetwist:local";
const port = process.env.FILETWIST_TEST_PORT || "18765";
if (!/^[a-zA-Z0-9][a-zA-Z0-9._/@:-]*$/.test(image)) {
  throw new Error("FILETWIST_TEST_IMAGE must be a Docker image reference");
}
if (!/^\d+$/.test(port) || Number(port) < 1024 || Number(port) > 65535) {
  throw new Error("FILETWIST_TEST_PORT must be a port between 1024 and 65535");
}
const baseURL = `http://127.0.0.1:${port}`;

export default defineConfig({
  testDir: ".",
  testMatch: "*.spec.js",
  fullyParallel: false,
  workers: 1,
  forbidOnly: Boolean(process.env.CI),
  retries: 0,
  timeout: 120_000,
  expect: { timeout: 30_000 },
  reporter: "list",
  use: {
    baseURL,
    browserName: "chromium",
    actionTimeout: 15_000,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  webServer: {
    command: [
      "docker run --rm --init --platform linux/amd64",
      "--read-only --tmpfs /tmp:size=512m,mode=1777",
      "--tmpfs /data:size=256m,uid=10001,gid=10001,mode=0750",
      "--cap-drop ALL --security-opt no-new-privileges:true",
      `--publish 127.0.0.1:${port}:8080`,
      "--env ACCELERATION=cpu --env MIN_FREE_SPACE=1MiB",
      "--env MAX_CONCURRENT_PROCESSES=1",
      image,
    ].join(" "),
    url: `${baseURL}/healthz`,
    reuseExistingServer: false,
    timeout: 90_000,
    gracefulShutdown: { signal: "SIGTERM", timeout: 35_000 },
  },
});
