// Smoke test for the BUILT site: boots Cloudflare Pages' own local simulator
// (`wrangler pages dev out`) on a free port and checks the routing contract
// that no unit of the build verifies on its own — real status codes, the
// _redirects canonicalization (308), the _headers types on the extensionless
// artifacts, the static 404 (it depends on experimental.globalNotFound) and
// absolute share-image URLs. Using the actual Pages runtime means we assert
// the deployed behavior, not a re-implementation of it.
//
// The static export deliberately has NO server-side language negotiation
// (the former proxy.ts could not survive `output: "export"`): "/" is English
// for every browser and Chinese users switch via the header switcher. This
// script asserts that too, so the decision stays documented instead of
// silently regressing.
// Usage: npm run build && node scripts/smoke.mjs
import { spawn } from "node:child_process";
import { createServer } from "node:net";
import { existsSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const ORIGIN = "https://supacove.com";
const OUT = join(fileURLToPath(new URL(".", import.meta.url)), "..", "out");

if (!existsSync(join(OUT, "index.html"))) {
  console.error("smoke: out/ is missing or unbuilt — run `npm run build` first");
  process.exit(1);
}

const port = await new Promise((resolve, reject) => {
  const probe = createServer();
  probe.once("error", reject);
  probe.listen(0, "127.0.0.1", () => {
    const { port } = probe.address();
    probe.close(() => resolve(port));
  });
});
const base = `http://127.0.0.1:${port}`;

const server = spawn("npx", ["wrangler", "pages", "dev", "out", "--port", String(port), "--ip", "127.0.0.1", "--compatibility-date=2025-05-05"], {
  stdio: ["ignore", "ignore", "inherit"],
  detached: true,
});
const kill = (signal) => {
  try {
    process.kill(-server.pid, signal);
  } catch {}
};
// SIGKILL is the synchronous last resort; finish() below is the orderly path.
process.on("exit", () => kill("SIGKILL"));
for (const signal of ["SIGINT", "SIGTERM"]) process.on(signal, () => process.exit(1));

/** Stops the simulator (SIGTERM, then SIGKILL after 3 s) and exits. */
async function finish(code) {
  if (server.exitCode === null && server.signalCode === null) {
    const gone = new Promise((resolve) => server.once("exit", resolve));
    kill("SIGTERM");
    await Promise.race([gone, new Promise((r) => setTimeout(r, 5000))]);
  }
  process.exit(code);
}
// wrangler can take a while on first boot (workerd spawn); a hung request
// must still not hang CI.
setTimeout(() => {
  console.error("smoke: timed out after 120 s");
  finish(1);
}, 120_000).unref();

// The timeout also covers reading the body.
const get = (path, headers = {}) =>
  fetch(base + path, { redirect: "manual", headers, signal: AbortSignal.timeout(10_000) });

let ready = false;
for (let i = 0; i < 120 && !ready; i++) {
  try {
    ready = (await fetch(base + "/robots.txt", { signal: AbortSignal.timeout(2000) })).ok;
  } catch {}
  if (!ready) await new Promise((r) => setTimeout(r, 500));
}
if (!ready) {
  console.error(`smoke: wrangler never became ready on :${port}`);
  await finish(1);
}

const failures = [];
let checks = 0;
function check(name, ok, detail = "") {
  checks++;
  if (!ok) failures.push(detail ? `${name} — ${detail}` : name);
}

// 200s (real pages and the metadata routes)
for (const path of ["/", "/zh", "/docs", "/zh/docs", "/docs/guides/supabase-backup", "/zh/docs/guides/supabase-backup", "/sitemap.xml", "/robots.txt"]) {
  const res = await get(path);
  check(`${path} is 200`, res.status === 200, `got ${res.status}`);
}

// /en… → 308 to the unprefixed URL, via public/_redirects (Pages cannot set
// cookies from _redirects, so the old sc_lang=en expectation is gone).
for (const [path, target] of [
  ["/en", "/"],
  ["/en/docs", "/docs"],
  ["/en/docs/quickstart?ref=x", "/docs/quickstart?ref=x"],
]) {
  const res = await get(path, { "accept-language": "zh-CN,zh;q=0.9" });
  const location = res.headers.get("location") ?? "";
  check(`${path} is 308`, res.status === 308, `got ${res.status}`);
  check(`${path} → ${target}`, location === target || location === base + target, `Location: ${location}`);
}

// "/" is English for EVERY browser: a static export has no server-side
// Accept-Language negotiation (documented decision, see README).
{
  const zh = { "accept-language": "zh-CN,zh;q=0.9,en;q=0.8" };
  check("zh browser at / is 200 (no negotiation)", (await get("/", zh)).status === 200);
  check("zh browser at /?utm_source=x is 200", (await get("/?utm_source=x", zh)).status === 200);
}

// 404s: real status, the static bilingual document, not indexable
for (const path of ["/nope", "/zh/nope", "/docs/nope", "/zh/docs/nope"]) {
  const res = await get(path);
  const html = await res.text();
  check(`${path} is 404`, res.status === 404, `got ${res.status}`);
  check(`${path} renders the 404 heading in HTML`, /<h1[^>]*>No backup of this page\.<\/h1>/.test(html));
  check(`${path} renders the Chinese 404 heading in HTML`, html.includes("这一页没有备份。"));
  check(`${path} is noindex`, /<meta name="robots" content="noindex/.test(html));
}

// Share image: absolute production URLs in the markup, a PNG behind them
for (const path of ["/", "/zh", "/docs/quickstart", "/zh/docs/guides/supabase-backup"]) {
  const html = await (await get(path)).text();
  for (const property of ['property="og:image"', 'name="twitter:image"']) {
    const url = html.match(new RegExp(`<meta ${property} content="([^"]+)"`))?.[1] ?? "";
    check(`${path} ${property} is absolute`, url.startsWith(`${ORIGIN}/opengraph-image`), `got "${url}"`);
  }
  check(`${path} has no localhost URL`, !html.includes("localhost:3000"));
}

// public/_headers: extensionless artifacts need explicit types, and the
// baked search index stays out of search engines.
{
  const res = await get("/opengraph-image");
  check("/opengraph-image is a PNG", res.status === 200 && res.headers.get("content-type") === "image/png", `${res.status} ${res.headers.get("content-type")}`);
}
{
  const res = await get("/api/search");
  check("/api/search is JSON", res.headers.get("content-type") === "application/json", `got ${res.headers.get("content-type")}`);
  check("/api/search is noindex", (res.headers.get("x-robots-tag") ?? "").includes("noindex"), `got ${res.headers.get("x-robots-tag")}`);
  const body = await res.json().catch(() => null);
  // orama export shape: {type:"advanced", i18n, index:{…}, docs:{docs:{…}, count}}
  check("/api/search is the baked orama index",
    body?.type === "advanced" && body?.i18n === true && body?.docs?.count > 0 && body?.index?.indexes,
    `type=${body?.type} i18n=${body?.i18n} docs.count=${body?.docs?.count}`);
}

if (failures.length) {
  console.error(`smoke: ${failures.length} of ${checks} checks failed`);
  for (const failure of failures) console.error(`  ✗ ${failure}`);
  await finish(1);
}
console.log(`smoke: ${checks} checks passed`);
await finish(0);
