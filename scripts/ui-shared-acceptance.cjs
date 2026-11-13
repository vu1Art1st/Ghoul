const fs = require("node:fs/promises");
const path = require("node:path");
const { chromium } = require(process.env.PLAYWRIGHT_PATH || "/usr/lib/node_modules/playwright");

const base = process.env.PREVIEW_BASE || "http://127.0.0.1:3017";
const outDir = process.env.UI_OUT || ".tmp/ui-check";

async function inspectPage(page, name) {
  const metrics = await page.evaluate(() => ({
    text: document.body.innerText,
    scrollWidth: document.documentElement.scrollWidth,
    clientWidth: document.documentElement.clientWidth,
  }));
  if (metrics.text.includes("Runtime TypeError")) {
    throw new Error(`${name}: runtime error page`);
  }
  if (metrics.scrollWidth > metrics.clientWidth + 1) {
    throw new Error(`${name}: horizontal overflow ${metrics.scrollWidth}/${metrics.clientWidth}`);
  }
  await page.screenshot({ path: path.join(outDir, `${name}.png`), fullPage: true });
  return { name, width: metrics.scrollWidth };
}

async function runViewport(browser, label, viewport) {
  const page = await browser.newPage({ viewport });
  const results = [];
  try {
    await page.goto(`${base}/function/shared/`, { waitUntil: "networkidle" });
    await page.getByText("验证码识别器").waitFor();
    results.push(await inspectPage(page, `shared-${label}`));

    await page.goto(`${base}/function/shared/detail/?id=1`, { waitUntil: "networkidle" });
    await page.getByRole("heading", { name: "验证码识别器" }).waitFor();
    await page.getByRole("tab", { name: "文件" }).click();
    await page.getByText("recognize.py").click();
    await page.getByText("print('shared demo asset')").waitFor();
    results.push(await inspectPage(page, `detail-files-${label}`));

    await page.getByRole("tab", { name: "版本" }).click();
    await page.getByText("tasks/4/shared/recognize.py").first().waitFor();
    results.push(await inspectPage(page, `detail-versions-${label}`));
    return results;
  } finally {
    await page.close();
  }
}

async function main() {
  await fs.mkdir(outDir, { recursive: true });
  const browser = await chromium.launch({ headless: true });
  try {
    const results = [
      ...(await runViewport(browser, "desktop", { width: 1440, height: 1100 })),
      ...(await runViewport(browser, "mobile", { width: 390, height: 844 })),
    ];
    console.log(JSON.stringify({ ok: true, results }, null, 2));
  } finally {
    await browser.close();
  }
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
