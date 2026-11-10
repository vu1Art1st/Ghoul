import { writeFile } from "node:fs/promises";

const endpoint = process.env.CDP_ENDPOINT ?? "http://127.0.0.1:9223";
const base = process.env.PREVIEW_BASE ?? "http://localhost:3017";
const outDir = process.env.UI_OUT ?? ".tmp";

async function openPage() {
  const response = await fetch(`${endpoint}/json/new?about:blank`, { method: "PUT" });
  if (!response.ok) throw new Error(`create target: ${response.status}`);
  return response.json();
}

async function connect(wsURL) {
  const socket = new WebSocket(wsURL);
  await new Promise((resolve, reject) => {
    socket.addEventListener("open", resolve, { once: true });
    socket.addEventListener("error", reject, { once: true });
  });
  let seq = 0;
  const pending = new Map();
  socket.addEventListener("message", (event) => {
    const message = JSON.parse(event.data);
    if (!message.id) return;
    const callback = pending.get(message.id);
    if (!callback) return;
    pending.delete(message.id);
    if (message.error) callback.reject(new Error(message.error.message));
    else callback.resolve(message.result);
  });
  return {
    close: () => socket.close(),
    send(method, params = {}) {
      const id = ++seq;
      return new Promise((resolve, reject) => {
        pending.set(id, { resolve, reject });
        socket.send(JSON.stringify({ id, method, params }));
      });
    },
  };
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function inspect(cdp, name, path, width, height, expected) {
  await cdp.send("Emulation.setDeviceMetricsOverride", {
    width, height, deviceScaleFactor: 1, mobile: width < 600,
  });
  await cdp.send("Page.navigate", { url: `${base}${path}` });
  await sleep(2200);
  const result = await cdp.send("Runtime.evaluate", {
    expression: `(() => ({
      text: document.body.innerText,
      title: document.title,
      path: location.pathname,
      scrollWidth: document.documentElement.scrollWidth,
      clientWidth: document.documentElement.clientWidth,
      buttons: [...document.querySelectorAll("button")].map((button) => button.innerText.trim()).filter(Boolean)
    }))()`,
    returnByValue: true,
  });
  const page = result.result.value;
  const missing = expected.filter((value) => !page.text.includes(value));
  if (missing.length) throw new Error(`${name}: missing text ${missing.join(", ")}; got ${page.text.slice(0, 500)}`);
  if (page.scrollWidth > page.clientWidth + 1) throw new Error(`${name}: horizontal overflow ${page.scrollWidth}/${page.clientWidth}`);
  const shot = await cdp.send("Page.captureScreenshot", { format: "png", captureBeyondViewport: true });
  await writeFile(`${outDir}/${name}.png`, Buffer.from(shot.data, "base64"));
  return { name, path: page.path, viewport: `${width}x${height}`, width: page.scrollWidth, buttons: page.buttons.slice(0, 12) };
}

const target = await openPage();
const cdp = await connect(target.webSocketDebuggerUrl);
try {
  await cdp.send("Page.enable");
  await cdp.send("Runtime.enable");
  const results = [];
  results.push(await inspect(cdp, "shared-desktop", "/function/shared", 1440, 1100, ["共享空间", "captcha-recognizer", "ddddocr"]));
  results.push(await inspect(cdp, "shared-mobile", "/function/shared", 390, 844, ["共享空间", "资产库", "Python 依赖"]));
  results.push(await inspect(cdp, "hostenv-desktop", "/system/hostenv", 1440, 1100, ["宿主环境", "sqlmap", "安装任务"]));
  results.push(await inspect(cdp, "hostenv-mobile", "/system/hostenv", 390, 844, ["宿主环境", "工具", "安装代理"]));
  await cdp.send("Emulation.setDeviceMetricsOverride", { width: 1440, height: 1100, deviceScaleFactor: 1, mobile: false });
  await cdp.send("Page.navigate", { url: `${base}/function/shared` });
  await sleep(1800);
  const clicked = await cdp.send("Runtime.evaluate", {
    expression: `(() => {
      const button = [...document.querySelectorAll("button")].find((item) => item.innerText.trim() === "发布资产");
      if (!button) return false;
      button.click();
      return true;
    })()`,
    returnByValue: true,
  });
  await sleep(300);
  const dialog = await cdp.send("Runtime.evaluate", {
    expression: `({ hasButton: Boolean(${clicked.result.value}), text: document.body.innerText.includes("发布前请移除目标地址") })`,
    returnByValue: true,
  });
  if (!dialog.result.value.hasButton || !dialog.result.value.text) throw new Error(`publish dialog failed: ${JSON.stringify(dialog.result.value)}`);
  results.push({ name: "publish-dialog", action: "open", ok: true });
  console.log(JSON.stringify({ ok: true, results }, null, 2));
} finally {
  cdp.close();
  await fetch(`${endpoint}/json/close/${target.id}`);
}
