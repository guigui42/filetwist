import { chromium, test as base, expect } from "@playwright/test";
import { fileURLToPath } from "node:url";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";

const photo = fileURLToPath(new URL("../../fixtures/generated/rgba-2x2.png", import.meta.url));
const video = fileURLToPath(new URL("../../fixtures/generated/h264-aac-32x24.mp4", import.meta.url));
const silentVideo = fileURLToPath(new URL("../../fixtures/generated/h264-silent-32x24.mp4", import.meta.url));

const test = base.extend({
  jobs: async ({ request }, use) => {
    const ids = new Set();
    await use(ids);
    for (const id of ids) {
      const response = await request.post(`/jobs/${id}/delete`);
      expect([200, 404]).toContain(response.status());
    }
  },
  page: async ({ page }, use) => {
    const errors = [];
    page.on("pageerror", (error) => errors.push(error.message));
    await use(page);
    expect(errors).toEqual([]);
  },
});

async function upload(page, jobs, files = photo) {
  await page.goto("/");
  await page.getByLabel("Files to convert", { exact: true }).setInputFiles(files);
  await page.getByRole("button", { name: "Upload", exact: true }).click();
  await expect(page.getByRole("button", { name: /^Convert \d+ file/ })).toBeVisible();
  const action = await page.locator("#job-form").getAttribute("hx-post");
  expect(action).toMatch(/^\/jobs\/[A-Za-z0-9_-]{22}\/start$/);
  const id = action.split("/")[2];
  jobs.add(id);
  return id;
}

async function readDownload(download) {
  expect(await download.failure()).toBeNull();
  const stream = await download.createReadStream();
  const chunks = [];
  for await (const chunk of stream) {
    chunks.push(chunk);
  }
  return { name: download.suggestedFilename(), bytes: Buffer.concat(chunks) };
}

async function downloadBytes(page, link) {
  const pending = page.waitForEvent("download");
  await link.click();
  return readDownload(await pending);
}

test("upload, choose a profile, convert, download, and delete", async ({ page, jobs }) => {
  const id = await upload(page, jobs);
  await page.getByLabel("Operation", { exact: true }).selectOption("lossless_image");
  await page.getByRole("button", { name: "Convert 1 file", exact: true }).click();
  await expect(page.locator("#job")).toHaveAttribute("data-job-state", "completed");

  const output = await downloadBytes(page, page.getByRole("link", { name: /^Download rgba-2x2-lossless\.png/ }));
  expect(output.name).toBe("rgba-2x2-lossless.png");
  expect(output.bytes.subarray(0, 8)).toEqual(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]));

  const archive = await downloadBytes(page, page.getByRole("link", { name: "Download all as ZIP", exact: true }));
  expect(archive.name).toMatch(/\.zip$/);
  expect(archive.bytes.subarray(0, 4)).toEqual(Buffer.from([80, 75, 3, 4]));
  expect(archive.bytes.includes(Buffer.from(output.name))).toBe(true);
  await expect(page.getByRole("button", { name: "Download all files", exact: true })).toHaveCount(0);

  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "Delete job", exact: true }).click();
  await expect(page.getByText("The job and all of its files were deleted.", { exact: true })).toBeVisible();
  jobs.delete(id);
});

test("download all files starts each browser download", async ({ jobs }, testInfo) => {
  const profile = testInfo.outputPath("chromium-profile");
  await mkdir(join(profile, "Default"), { recursive: true });
  await writeFile(join(profile, "Default", "Preferences"), JSON.stringify({
    profile: {
      default_content_setting_values: {
        automatic_downloads: 1,
      },
    },
  }));
  const context = await chromium.launchPersistentContext(profile, {
    acceptDownloads: true,
    baseURL: testInfo.project.use.baseURL,
    headless: true,
  });
  const pages = context.pages();
  const page = pages.length > 0 ? pages[0] : await context.newPage();
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));

  const photoBytes = await readFile(photo);
  const firstPhoto = {
    name: "rgba-2x2.png",
    mimeType: "image/png",
    buffer: photoBytes,
  };
  const secondPhoto = {
    name: "second.png",
    mimeType: "image/png",
    buffer: photoBytes,
  };
  await upload(page, jobs, [firstPhoto, secondPhoto]);
  const operations = page.getByLabel("Operation", { exact: true });
  await operations.nth(0).selectOption("lossless_image");
  await operations.nth(1).selectOption("lossless_image");
  await page.getByRole("button", { name: "Convert 2 files", exact: true }).click();
  await expect(page.locator("#job")).toHaveAttribute("data-job-state", "completed");

  const targets = await page.locator("[data-download-file]").evaluateAll(
    (links) => links.map((link) => link.href).sort()
  );
  const downloads = [];
  page.on("download", (download) => downloads.push(download));
  await page.getByRole("button", { name: "Download all files", exact: true }).click();
  await expect.poll(() => downloads.length).toBe(2);
  await expect(page.locator("#job-notice")).toContainText("allow multiple downloads");
  expect(downloads.map((download) => download.url()).sort()).toEqual(targets);

  const outputs = await Promise.all(downloads.map(readDownload));
  expect(outputs.map((output) => output.name).sort()).toEqual([
    "rgba-2x2-lossless.png",
    "second-lossless.png",
  ]);
  for (const output of outputs) {
    expect(output.bytes.subarray(0, 8)).toEqual(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]));
  }
  await expect(page.getByRole("link", { name: "Download all as ZIP", exact: true })).toBeVisible();
  expect(errors).toEqual([]);
  await context.close();
});

test("failed start keeps the selection and supports retry", async ({ page, jobs }) => {
  const id = await upload(page, jobs);
  await page.getByLabel("Operation", { exact: true }).selectOption("smaller_photo");
  await page.route(`**/jobs/${id}/start`, (route) => route.fulfill({
    status: 503,
    contentType: "text/html",
    body: '<p role="alert">The conversion queue is full. Try again shortly.</p>',
  }), { times: 1 });
  await page.getByRole("button", { name: "Convert 1 file", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("The conversion queue is full.");
  await expect(page.locator("#job-notice")).toContainText("The conversion queue is full.");
  await expect(page.locator("#job-notice")).toBeFocused();
  await expect(page.getByLabel("Operation", { exact: true })).toHaveValue("smaller_photo");
  await page.getByRole("button", { name: "Convert 1 file", exact: true }).click();
  await expect(page.locator("#job")).toHaveAttribute("data-job-state", "completed");
  await expect(page.getByRole("link", { name: /^Download rgba-2x2-smaller\.webp/ })).toBeVisible();
});

test("silent video does not offer audio extraction", async ({ page, jobs }) => {
  await upload(page, jobs, silentVideo);
  await expect(page.getByLabel("Operation", { exact: true }).locator("option")).toHaveText([
    "Compatible video (MP4)",
    "Smaller video (MP4)",
  ]);
});

test("cancel active work and delete the job", async ({ page, jobs }) => {
  // Keep a queue of real conversions so cancellation does not race a single tiny file.
  const id = await upload(page, jobs, Array(12).fill(video));
  await page.getByRole("button", { name: "Convert 12 files", exact: true }).click();
  await page.getByRole("button", { name: "Cancel", exact: true }).click();
  await expect(page.locator("#job")).toHaveAttribute("data-job-state", "canceled");
  await expect(page.getByRole("button", { name: "Cancel", exact: true })).toHaveCount(0);
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "Delete job", exact: true }).click();
  await expect(page.getByText("The job and all of its files were deleted.", { exact: true })).toBeVisible();
  jobs.delete(id);
});

test("shared-workspace guidance and local help are available before uploading", async ({ page }) => {
  await page.goto("/");
  await expect(page.locator(".privacy-note")).toContainText("Everyone with access");
  await expect(page.locator(".privacy-note")).toContainText("delete");
  await page.getByRole("link", { name: "How your files are handled", exact: true }).click();
  await expect(page).toHaveURL(/\/help#privacy$/);
  await expect(page.getByRole("heading", { name: "Shared workspace", exact: true })).toBeVisible();
  await expect(page.getByText("Keep your originals.", { exact: true })).toBeVisible();
  await expect(page.locator(".profile-guide dt")).toHaveCount(8);
});

test("batch presets respect eligibility and individual choices, including after removal", async ({ page, jobs }) => {
  await upload(page, jobs, [photo, photo, silentVideo]);
  await expect(page.locator("#upload-section")).not.toHaveAttribute("open");
  await expect(page.locator("#job-heading")).toBeFocused();
  await expect(page).toHaveURL(/\/jobs\/[A-Za-z0-9_-]{22}$/);

  const choices = page.getByLabel("Operation", { exact: true });
  await choices.nth(0).selectOption("lossless_image");
  await expect(page.locator(".operation-help").nth(0)).toContainText("not an original-file copy");
  await page.locator("#batch-tools > summary").click();
  await page.getByLabel("Batch preset", { exact: true }).selectOption("smaller_photo");
  await page.getByRole("button", { name: "Apply to compatible files", exact: true }).click();
  await expect(choices.nth(0)).toHaveValue("lossless_image");
  await expect(choices.nth(1)).toHaveValue("smaller_photo");
  await expect(choices.nth(2)).toHaveValue("compatible_video");
  await expect(page.locator("#batch-status")).toContainText("Applied to 1 file. Kept 1 individual choice. 1 file did not support this preset.");

  page.once("dialog", dialog => dialog.accept());
  await page.locator("[data-remove-file]").nth(2).click();
  await expect(choices).toHaveCount(2);
  await expect(choices.nth(0)).toHaveValue("lossless_image");
  await expect(choices.nth(1)).toHaveValue("smaller_photo");
  await expect(page.locator("#batch-tools")).toHaveAttribute("open");
  await expect(page.locator("#job-heading")).toBeFocused();

  await page.getByLabel("Batch preset", { exact: true }).selectOption("compatible_photo");
  await page.getByLabel("Keep individual choices", { exact: true }).uncheck();
  await page.getByRole("button", { name: "Apply to compatible files", exact: true }).click();
  await expect(choices.nth(0)).toHaveValue("compatible_photo");
  await expect(choices.nth(1)).toHaveValue("compatible_photo");
  await page.getByLabel("Keep individual choices", { exact: true }).check();
  await page.getByLabel("Batch preset", { exact: true }).selectOption("smaller_photo");
  await page.getByRole("button", { name: "Apply to compatible files", exact: true }).click();
  await expect(page.locator("#batch-status")).toContainText("Applied to 2 files. Kept 0 individual choices.");
});

test("removing the last pending file returns to an empty upload workflow", async ({ page, jobs }) => {
  const id = await upload(page, jobs);
  page.once("dialog", dialog => dialog.accept());
  await page.locator("[data-remove-file]").click();
  await expect(page.getByRole("alert")).toContainText("empty job was deleted");
  await expect(page.locator("#job")).toHaveCount(0);
  await expect(page.locator("#upload-section")).toHaveAttribute("open");
  await expect(page).toHaveURL(/\/$/);
  expect((await page.request.get(`/jobs/${id}`)).status()).toBe(404);
  jobs.delete(id);
});

test("selected files can be removed before upload", async ({ page, jobs }) => {
  await page.goto("/");
  await page.getByLabel("Files to convert", { exact: true }).setInputFiles([photo, silentVideo]);
  await page.locator("#selected-files").getByRole("button", { name: /Remove h264-silent/ }).click();
  await expect(page.locator("#file-list")).toHaveText(/1 file selected/);
  await expect(page.locator("#selected-files li")).toHaveCount(1);
  await page.getByRole("button", { name: "Upload", exact: true }).click();
  await expect(page.getByRole("button", { name: "Convert 1 file", exact: true })).toBeVisible();
  jobs.add(new URL(page.url()).pathname.split("/")[2]);
});

test("upload limits give actionable feedback before sending files", async ({ page }) => {
  await page.goto("/");
  await page.getByLabel("Files to convert", { exact: true }).setInputFiles(Array(26).fill(photo));
  await expect(page.getByRole("button", { name: "Upload", exact: true })).toBeDisabled();
  await expect(page.locator("#upload-notice")).toContainText("Select at most 25 files");
  await page.locator("#selected-files button").first().click();
  await expect(page.getByRole("button", { name: "Upload", exact: true })).toBeEnabled();
  await expect(page.locator("#upload-notice")).toBeEmpty();
});

test("mobile results prioritize downloads and keep technical details optional", async ({ page, jobs }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  const name = `synthetic-${"longfilename".repeat(7)}.png`;
  await page.getByLabel("Files to convert", { exact: true }).setInputFiles({
    name, mimeType: "image/png", buffer: await readFile(photo),
  });
  await page.getByRole("button", { name: "Upload", exact: true }).click();
  await expect(page.getByRole("button", { name: "Convert 1 file", exact: true })).toBeVisible();
  const id = new URL(page.url()).pathname.split("/")[2];
  jobs.add(id);
  expect(await page.getByLabel("Operation", { exact: true }).evaluate(node => node.getBoundingClientRect().height)).toBeGreaterThanOrEqual(44);
  const filesBox = await page.locator("#job-form > .files").boundingBox();
  const actionsBox = await page.locator("#job-form > .actions").boundingBox();
  expect(actionsBox.y).toBeGreaterThanOrEqual(filesBox.y + filesBox.height);
  await page.getByRole("button", { name: "Convert 1 file", exact: true }).click();
  await expect(page.locator("#job")).toHaveAttribute("data-job-state", "completed");
  await expect(page.locator("#job-heading")).toHaveText("Your downloads are ready");
  await expect(page.locator(".primary-download")).toHaveText("Download all as ZIP");
  await expect(page.locator("[data-file-details]")).not.toHaveAttribute("open");
  await expect(page.getByRole("link", { name: /^Download synthetic-/ })).toHaveText(/Download file/);
  for (const colorScheme of ["light", "dark"]) {
    await page.emulateMedia({ colorScheme });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    const heights = await page.locator("#job button, #job a.button").evaluateAll(nodes => nodes.map(node => node.getBoundingClientRect().height));
    expect(heights.every(height => height >= 44)).toBe(true);
  }
  await page.locator("[data-file-details] > summary").click();
  await expect(page.getByText("Validation: passed", { exact: true })).toBeVisible();
  await page.goto("/");
  await expect(page.locator(".recent-jobs h3")).toContainText(name);
});

for (const action of ["remove last file", "delete job"]) {
  test(`direct job page returns home after ${action}`, async ({ page, jobs }) => {
    const id = await upload(page, jobs);
    await page.goto(`/jobs/${id}`);
    await expect(page.locator("#upload-section")).toHaveCount(0);
    page.once("dialog", dialog => dialog.accept());
    if (action === "remove last file") {
      await page.locator("[data-remove-file]").click();
    } else {
      await page.getByRole("button", { name: "Delete job", exact: true }).click();
    }
    await expect(page).toHaveURL(/\/$/);
    await expect(page.locator("#upload-section")).toHaveAttribute("open");
    expect((await page.reload()).status()).toBe(200);
    await expect(page.getByRole("button", { name: "Upload", exact: true })).toBeVisible();
    expect((await page.request.get(`/jobs/${id}`)).status()).toBe(404);
    jobs.delete(id);
  });
}

test("total-byte upload limit recovers when the extra file is removed", async ({ page }) => {
  await page.goto("/");
  const bytes = (await readFile(photo)).length;
  await page.locator("#upload-form").evaluate((form, limit) => {
    form.dataset.maxBytes = String(limit);
  }, bytes);
  await page.getByLabel("Files to convert", { exact: true }).setInputFiles([photo, photo]);
  await expect(page.getByRole("button", { name: "Upload", exact: true })).toBeDisabled();
  await expect(page.locator("#upload-notice")).toContainText(`totaling no more than ${bytes} B`);
  await page.locator("#selected-files button").first().click();
  await expect(page.getByRole("button", { name: "Upload", exact: true })).toBeEnabled();
  await expect(page.locator("#upload-notice")).toBeEmpty();
});

test("upload cancellation is transfer-only and its listener is registered once", async ({ page }) => {
  await page.addInitScript(() => {
    window.uploadTransport = { requests: [], cancelListeners: 0 };
    const addListener = EventTarget.prototype.addEventListener;
    EventTarget.prototype.addEventListener = function (type, listener, options) {
      if (this.id === "upload-cancel" && type === "click") {
        window.uploadTransport.cancelListeners++;
      }
      return addListener.call(this, type, listener, options);
    };
    // Control the transfer/inspection boundary without relying on network timing.
    window.XMLHttpRequest = class extends EventTarget {
      constructor() {
        super();
        this.upload = new EventTarget();
        this.aborts = 0;
        this.status = 0;
        this.responseText = '<p role="status">Upload finished.</p>';
      }
      open() {}
      setRequestHeader() {}
      getResponseHeader() { return "text/html"; }
      send() { window.uploadTransport.requests.push(this); }
      abort() {
        this.aborts++;
        this.dispatchEvent(new Event("abort"));
      }
    };
  });
  await page.goto("/");
  for (let index = 0; index < 2; index++) {
    await page.getByLabel("Files to convert", { exact: true }).setInputFiles(photo);
    await page.getByRole("button", { name: "Upload", exact: true }).click();
    await expect(page.getByRole("button", { name: "Cancel upload", exact: true })).toBeVisible();
    await page.evaluate(() => window.uploadTransport.requests.at(-1).upload.dispatchEvent(new Event("load")));
    await expect(page.locator("#upload-cancel")).toBeHidden();
    await expect(page.locator("#upload-progress-text")).toHaveText("Upload sent. Checking file types...");
    await page.locator("#upload-cancel").evaluate(button => button.click());
    expect(await page.evaluate(() => window.uploadTransport.requests.at(-1).aborts)).toBe(0);
    await page.evaluate(() => {
      const request = window.uploadTransport.requests.at(-1);
      request.status = 200;
      request.dispatchEvent(new Event("load"));
    });
    await expect(page.locator("#file-list")).toHaveText("No files selected.");
  }
  expect(await page.evaluate(() => window.uploadTransport.cancelListeners)).toBe(1);
  await page.getByLabel("Files to convert", { exact: true }).setInputFiles(photo);
  await page.getByRole("button", { name: "Upload", exact: true }).click();
  await page.getByRole("button", { name: "Cancel upload", exact: true }).click();
  await expect(page.locator("#upload-progress-text")).toContainText("Upload canceled.");
  await expect(page.getByRole("button", { name: "Upload", exact: true })).toBeEnabled();
  await expect(page.locator("#file-list")).toHaveText(/1 file selected/);
  expect(await page.evaluate(() => window.uploadTransport.requests.at(-1).aborts)).toBe(1);
});
