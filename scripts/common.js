// PandaScript runtime: only Page and console, not Node.js or a browser global.
// This function is serialized into the page; it must be self-contained.
function collectResults(config, limit) {
  const clean = (text) => (text || "").replace(/\s+/g, " ").trim();
  const first = (root, selectors) => {
    for (const selector of selectors) {
      const node = root.querySelector(selector);
      if (node) return node;
    }
    return null;
  };
  const title = clean(document.title);
  const url = window.location.href;
  const text = clean(document.body && document.body.textContent);
  const challenge = document.querySelector(
    '#challenge-form, #anomaly-modal, #captcha-form, ' +
    'iframe[src*="captcha-delivery.com"], #cf-chl-widget, #challenge-running, #anubis-main'
  );
  if (challenge || /\/captcha(?:\/|\?|$)/i.test(url) ||
      /^(?:captcha|smartcaptcha)$/i.test(title) ||
      /access denied|just a moment|robot check|403\s*[-–:]?\s*forbidden|доступ ограничен/i.test(title)) {
    return {status: "blocked", url, title, error: "Search provider returned an anti-bot challenge", results: []};
  }
  const results = [];
  const seen = new Set();
  for (const row of document.querySelectorAll(config.rows)) {
    // Do not turn sponsored cards into organic search results.
    if (row.matches(config.ads || '[data-pandaxng-never]') || row.closest('[data-text-ad], .result--ad')) continue;
    const link = first(row, config.links);
    if (!link) continue;
    const heading = first(row, config.titles);
    const headingText = clean((heading || link).textContent);
    const href = link.href;
    if (!headingText || !href || !/^https?:\/\//i.test(href) || seen.has(href)) continue;
    const snippet = first(row, config.snippets);
    seen.add(href);
    results.push({url: href, title: headingText, content: clean(snippet && snippet.textContent)});
    if (results.length >= limit) break;
  }
  if (results.length) return {status: "ok", url, title, results};
  if (/unusual traffic|verify (?:that )?you are (?:a )?human|verifying your request|automated (?:queries|requests)|confirm you are human|подтвердите,? что вы не робот/i.test(text)) {
    return {status: "blocked", url, title, error: "Search provider requires verification", results: []};
  }
  if ((config.empty || []).some((pattern) => new RegExp(pattern, "i").test(text))) {
    return {status: "empty", url, title, results: []};
  }
  return {status: "parse_error", url, title, error: "No result cards matched; layout changed, incomplete page or unrecognized block", results: []};
}

async function search(config) {
  const page = new Page();
  try {
    await page.goto(request.url, {timeout: request.timeout, waitUntil: request.waitUntil});
    // evaluate returns JSON text, unlike extract's native object return value.
    const script = `(${collectResults.toString()})(${JSON.stringify(config)}, ${request.limit})`;
    return JSON.parse(page.evaluate(script));
  } finally {
    // A failed goto can invalidate the page handle; don't mask its error.
    try { page.close(); } catch (_) {}
  }
}
