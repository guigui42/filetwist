import { test as base, expect } from "@playwright/test";
import { fileURLToPath } from "node:url";

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

async function downloadBytes(page, link) {
  const pending = page.waitForEvent("download");
  await link.click();
  const download = await pending;
  expect(await download.failure()).toBeNull();
  const stream = await download.createReadStream();
  const chunks = [];
  for await (const chunk of stream) {
    chunks.push(chunk);
  }
  return { name: download.suggestedFilename(), bytes: Buffer.concat(chunks) };
}

test("upload, choose a profile, convert, download, and delete", async ({ page, jobs }) => {
  const id = await upload(page, jobs);
  await page.getByLabel("Operation", { exact: true }).selectOption("lossless_image");
  await page.getByRole("button", { name: "Convert 1 file(s)", exact: true }).click();
  await expect(page.locator("#job")).toHaveAttribute("data-job-state", "completed");

  const output = await downloadBytes(page, page.getByRole("link", { name: /^Download rgba-2x2-lossless\.png/ }));
  expect(output.name).toBe("rgba-2x2-lossless.png");
  expect(output.bytes.subarray(0, 8)).toEqual(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]));

  const archive = await downloadBytes(page, page.getByRole("link", { name: "Download all as ZIP", exact: true }));
  expect(archive.name).toMatch(/\.zip$/);
  expect(archive.bytes.subarray(0, 4)).toEqual(Buffer.from([80, 75, 3, 4]));
  expect(archive.bytes.includes(Buffer.from(output.name))).toBe(true);

  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "Delete job", exact: true }).click();
  await expect(page.getByText("The job and all of its files were deleted.", { exact: true })).toBeVisible();
  jobs.delete(id);
});

test("failed start keeps the selection and supports retry", async ({ page, jobs }) => {
  const id = await upload(page, jobs);
  await page.getByLabel("Operation", { exact: true }).selectOption("smaller_photo");
  await page.route(`**/jobs/${id}/start`, (route) => route.fulfill({
    status: 503,
    contentType: "text/html",
    body: '<p role="alert">The conversion queue is full. Try again shortly.</p>',
  }), { times: 1 });
  await page.getByRole("button", { name: "Convert 1 file(s)", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("The conversion queue is full.");
  await expect(page.getByLabel("Operation", { exact: true })).toHaveValue("smaller_photo");
  await page.getByRole("button", { name: "Convert 1 file(s)", exact: true }).click();
  await expect(page.locator("#job")).toHaveAttribute("data-job-state", "completed");
  await expect(page.getByRole("link", { name: /^Download rgba-2x2-smaller\.webp/ })).toBeVisible();
});

test("silent video does not offer audio extraction", async ({ page, jobs }) => {
  await upload(page, jobs, silentVideo);
  await expect(page.getByLabel("Operation", { exact: true }).locator("option")).toHaveText([
    "Compatible video",
    "Smaller video",
  ]);
});

test("cancel active work and delete the job", async ({ page, jobs }) => {
  // Keep a queue of real conversions so cancellation does not race a single tiny file.
  const id = await upload(page, jobs, Array(12).fill(video));
  await page.getByRole("button", { name: "Convert 12 file(s)", exact: true }).click();
  await page.getByRole("button", { name: "Cancel", exact: true }).click();
  await expect(page.locator("#job")).toHaveAttribute("data-job-state", "canceled");
  await expect(page.getByRole("button", { name: "Cancel", exact: true })).toHaveCount(0);
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "Delete job", exact: true }).click();
  await expect(page.getByText("The job and all of its files were deleted.", { exact: true })).toBeVisible();
  jobs.delete(id);
});
