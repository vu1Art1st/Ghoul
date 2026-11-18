async function (page) {
  const cfg = __GHOUL_CAPTURE_CONFIG__;
  const manifest = {
    capture_engine: "playwright-cli",
    capture_mode: cfg.capture_mode || "native_action",
    page_url: cfg.page_url,
    source_url: cfg.source_url || "",
    started_at: new Date().toISOString(),
    route_rewrites: 0,
    responses: [],
  };
  const fingerprint = (value) => {
    let hash = 0x811c9dc5;
    for (let i = 0; i < value.length; i += 1) {
      hash ^= value.charCodeAt(i);
      hash = Math.imul(hash, 0x01000193);
    }
    return (hash >>> 0).toString(16).padStart(8, "0");
  };
  try {
    await page.setViewportSize({ width: cfg.viewport_width || 1440, height: cfg.viewport_height || 1000 });

    page.on("response", async (response) => {
      try {
        const url = response.url();
        if (cfg.route_match && !new RegExp(cfg.route_regex || "").test(url) && !url.includes(cfg.route_match)) {
          return;
        }
        manifest.responses.push({
          url,
          status: response.status(),
          content_type: (response.headers() || {})["content-type"] || "",
        });
      } catch {
        // Response metadata is supplementary; never fail the capture for it.
      }
    });

    if (cfg.route_match) {
      await page.route(cfg.route_match, async (route) => {
        const request = route.request();
        const url = new URL(request.url());
        for (const [key, value] of Object.entries(cfg.query || {})) {
          url.searchParams.set(key, String(value));
        }
        const headers = { ...request.headers(), ...(cfg.headers || {}) };
        if (cfg.content_type) {
          headers["content-type"] = cfg.content_type;
        }
        const options = { url: url.toString(), headers };
        if (cfg.http_method) {
          options.method = cfg.http_method;
        }
        if (cfg.request_body !== undefined && cfg.request_body !== null) {
          options.postData = cfg.request_body;
        }
        manifest.route_rewrites += 1;
        manifest.last_rewritten_url = url.toString();
        await route.continue(options);
      });
    }

    if (cfg.document_html) {
      await page.setContent(cfg.document_html, { waitUntil: "domcontentloaded", timeout: 45000 });
    } else {
      await page.goto(cfg.page_url, { waitUntil: "domcontentloaded", timeout: 45000 });
    }
    await page.waitForTimeout(250);

    if (cfg.prepare_js) {
      if (/(document\.(open|write|close)|(?:body|documentElement|document)\.(?:inner|outer)HTML\s*=|document\.(?:body|documentElement)\.(?:replaceChildren|replaceWith)|document\.createElement|insertAdjacentHTML|\bfetch\s*\(|\bXMLHttpRequest\b|\bWebSocket\b|location\s*=)/i.test(cfg.prepare_js)) {
        throw new Error("prepare_js may update page state but must not replace the original document");
      }
      await page.evaluate(cfg.prepare_js);
      await page.waitForTimeout(150);
    }

    const snapshot = async (selector) =>
      page.evaluate((selector) => ({
        url: location.href,
        title: document.title,
        selector_present: selector ? !!document.querySelector(selector) : true,
        body_children: document.body ? document.body.children.length : 0,
        html: document.documentElement ? document.documentElement.outerHTML : "",
      }), selector || "body");

    const before = await snapshot(cfg.precondition_selector || "body");
    if (!before.selector_present) {
      throw new Error(`precondition_selector is missing before the action: ${cfg.precondition_selector || "body"}`);
    }
    if (!before.body_children) {
      throw new Error("original page body is empty before the action");
    }

    if (cfg.capture_mode === "native_action" && cfg.action_selector) {
      await page.locator(cfg.action_selector).first().click({ timeout: 15000 });
    }
    if (cfg.wait_selector || cfg.capture_mode === "navigation_replay") {
      await page.waitForSelector(cfg.wait_selector || "body", { state: "visible", timeout: 30000 });
    }
    if (cfg.wait_text) {
      await page.waitForFunction(
        ({ selector, text }) => {
          const node = selector ? document.querySelector(selector) : document.body;
          return !!node && (node.innerText || node.textContent || "").includes(text);
        },
        { selector: cfg.wait_selector || "", text: cfg.wait_text },
        { timeout: 30000 },
      );
    }
    await page.waitForTimeout(500);

    const after = await snapshot(cfg.wait_selector || "body");
    if (before.title !== after.title) {
      throw new Error(`original page title changed during capture: ${before.title} -> ${after.title}`);
    }
    if (!after.selector_present) {
      throw new Error(`wait_selector disappeared during capture: ${cfg.wait_selector || "body"}`);
    }
    if (after.body_children < before.body_children) {
      throw new Error("original page DOM was replaced during capture");
    }
    if (cfg.route_match && cfg.capture_mode !== "raw_http_exchange" && manifest.route_rewrites === 0) {
      throw new Error(`no application request matched route: ${cfg.route_match}`);
    }

    await page.screenshot({
      path: cfg.screenshot_path,
      fullPage: !!cfg.full_page,
      animations: "disabled",
    });
    manifest.screenshot_path = cfg.screenshot_path;
    manifest.dom_before_fingerprint = fingerprint(before.html);
    manifest.dom_after_fingerprint = fingerprint(after.html);
    manifest.dom_changed = before.html !== after.html;
    manifest.page_title_before = before.title;
    manifest.page_title_after = after.title;
    manifest.body_children_before = before.body_children;
    manifest.body_children_after = after.body_children;
    manifest.wait_selector = cfg.wait_selector || "body";
    manifest.precondition_selector = cfg.precondition_selector || "body";
    manifest.wait_selector_present_before = before.selector_present;
    manifest.wait_selector_present_after = after.selector_present;
    manifest.completed_at = new Date().toISOString();
    manifest.ok = true;
    return manifest;
  } catch (error) {
    manifest.ok = false;
    manifest.error = error && error.stack ? error.stack : String(error);
    manifest.completed_at = new Date().toISOString();
    return manifest;
  }
}
