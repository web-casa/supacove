// Smoke test for the BUILT site: boots `next start` on a free port and checks
// the routing contract that no unit of the build verifies on its own —
// status codes, locale redirects, the server-rendered 404 (it depends on
// experimental.globalNotFound) and absolute share-image URLs.
// Usage: npx next build && node scripts/smoke.mjs
import { spawn } from "node:child_process";
import { createServer } from "node:net";

const ORIGIN = "https://supacove.com";

const port = await new Promise((resolve, reject) => {
  const probe = createServer();
  probe.once("error", reject);
  probe.listen(0, "127.0.0.1", () => {
    const { port } = probe.address();
    probe.close(() => resolve(port));
  });
});
const base = `http://127.0.0.1:${port}`;

const server = spawn("npx", ["next", "start", "-p", String(port), "-H", "127.0.0.1"], {
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

/** Stops the server (SIGTERM, then SIGKILL after 3 s) and exits. */
async function finish(code) {
  if (server.exitCode === null && server.signalCode === null) {
    const gone = new Promise((resolve) => server.once("exit", resolve));
    kill("SIGTERM");
    await Promise.race([gone, new Promise((r) => setTimeout(r, 3000))]);
  }
  process.exit(code);
}
// Nothing here should take long: a hung request must not hang CI.
setTimeout(() => {
  console.error("smoke: timed out after 120 s");
  finish(1);
}, 120_000).unref();

// The timeout also covers reading the body.
const get = (path, headers = {}) =>
  fetch(base + path, { redirect: "manual", headers, signal: AbortSignal.timeout(10_000) });

let ready = false;
for (let i = 0; i < 60 && !ready; i++) {
  try {
    ready = (await fetch(base + "/robots.txt", { signal: AbortSignal.timeout(2000) })).ok;
  } catch {}
  if (!ready) await new Promise((r) => setTimeout(r, 500));
}
if (!ready) {
  console.error(`smoke: server never became ready on :${port}`);
  await finish(1);
}

const failures = [];
let checks = 0;
function check(name, ok, detail = "") {
  checks++;
  if (!ok) failures.push(detail ? `${name} — ${detail}` : name);
}

// 200s
for (const path of ["/", "/zh", "/docs", "/zh/docs", "/docs/guides/supabase-backup", "/zh/docs/guides/supabase-backup", "/sitemap.xml", "/robots.txt"]) {
  const res = await get(path);
  check(`${path} is 200`, res.status === 200, `got ${res.status}`);
}

// /en… → 308 to the unprefixed URL, remembering English
for (const [path, target] of [
  ["/en", "/"],
  ["/en/docs", "/docs"],
  ["/en/docs/quickstart?ref=x", "/docs/quickstart?ref=x"],
]) {
  const res = await get(path, { "accept-language": "zh-CN,zh;q=0.9" });
  const location = res.headers.get("location") ?? "";
  check(`${path} is 308`, res.status === 308, `got ${res.status}`);
  check(`${path} → ${target}`, location === target || location === base + target, `Location: ${location}`);
  check(`${path} sets sc_lang=en`, (res.headers.get("set-cookie") ?? "").includes("sc_lang=en"));
}

// Language negotiation at "/"
{
  const zh = { "accept-language": "zh-CN,zh;q=0.9,en;q=0.8" };
  const res = await get("/?utm_source=x", zh);
  const location = res.headers.get("location") ?? "";
  check("zh browser at / is 307", res.status === 307, `got ${res.status}`);
  check("redirect keeps the query", location.endsWith("/zh?utm_source=x"), `Location: ${location}`);
  check("redirect varies on language and cookie", /accept-language/i.test(res.headers.get("vary") ?? ""));
  check("sc_lang=en suppresses the redirect", (await get("/", { ...zh, cookie: "sc_lang=en" })).status === 200);
  check("zh;q=0 is not a preference", (await get("/", { "accept-language": "zh;q=0, en" })).status === 200);
  check("en browser at / is 200", (await get("/", { "accept-language": "en-US,en;q=0.9" })).status === 200);
}

// 404s: real status, server-rendered body, not indexable
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
{
  const res = await get("/opengraph-image");
  check("/opengraph-image is a PNG", res.status === 200 && res.headers.get("content-type") === "image/png", `${res.status} ${res.headers.get("content-type")}`);
}

if (failures.length) {
  console.error(`smoke: ${failures.length} of ${checks} checks failed`);
  for (const failure of failures) console.error(`  ✗ ${failure}`);
  await finish(1);
}
console.log(`smoke: ${checks} checks passed`);
await finish(0);
