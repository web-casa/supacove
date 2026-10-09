# SupaCove documentation site

Fumadocs (Next.js) user documentation for SupaCove (https://supacove.com),
bilingual. English is the default and is served unprefixed (`/`, `/docs/…`)
from `app/(en)`; Chinese lives under `/zh` in `app/zh`. Both render the
shared implementations in `routes/`.

The site is a **pure static export** (`output: "export"` in
`next.config.mjs`) deployed to **Cloudflare Pages** — there is no Node
server at runtime. That constrains a few things:

- **No middleware.** The former `proxy.ts` (which redirected `/en/…` and
  auto-sent Chinese browsers from `/` to `/zh`) cannot run in a static
  export. `/en/…` canonicalization now lives in `public/_redirects`
  (308 → unprefixed). **Language negotiation is intentionally dropped:**
  `/` is English for every browser and Chinese users switch via the header
  switcher, which remembers the choice in the `sc_lang` cookie. This is a
  deliberate trade-off, asserted by the smoke test so it cannot silently
  regress.
- **Search is baked, not queried.** `app/api/search/route.ts` exports the
  whole Orama index at build time (`staticGET`); the dialog uses the
  `type: "static"` client (wired in `lib/providers.tsx`) that loads it once
  and searches client-side.
- **Response headers come from `public/_headers`**, since the extensionless
  `/opengraph-image` (PNG) and `/api/search` (JSON, noindex) artifacts can't
  set headers from a route handler in a static build.

The product was first called supabackup; the binary, image, metric names
and webhook headers now use the supacove name (pre-rename metric names and
headers are served as equal-valued aliases during the transition), and
`SB_*` variables keep their prefix.

```bash
npm ci
npm run dev        # http://localhost:3000 (next dev; middleware NOT emulated)
npm run build      # parity check + static export into out/
npm run check-parity   # both locales must ship the same slug set
npm run preview    # serve out/ with Cloudflare Pages' simulator (wrangler)
npm run smoke      # after a build: boots wrangler, checks routing/headers/404
```

### Deploying to Cloudflare Pages

Point a Pages project at this repo with:

| Setting | Value |
|---|---|
| Root directory | `docs-site` |
| Build command | `npm run build` |
| Build output directory | `out` |
| Node version | 22 |

`_redirects` and `_headers` in `out/` are honored by Pages automatically.
For "last updated" dates the docs read git history, so give the build a deep
enough clone (Pages' default shallow clone yields the commit date only).

Two things the build alone does not prove, both covered by
`scripts/smoke.mjs` (also run in CI, against `wrangler pages dev out`):

- **The 404 page** is `app/global-not-found.tsx`, enabled by
  `experimental.globalNotFound` in `next.config.mjs`. With one root layout
  per locale there is no shared layout a regular `not-found.tsx` could render
  in. It is one static document holding both languages; an inline script
  shows Chinese under `/zh`. Re-check it when upgrading Next.js.
- **The share image** is the route handler `app/opengraph-image/route.tsx`,
  not the `opengraph-image` file convention: at the app root that convention
  has no layout to take `metadataBase` from. Pages reference it through
  `OG_IMAGE` in `lib/site.ts`.

Content lives in `content/docs/{en,zh}/<slug>.mdx` (guides under `guides/`); each locale's `meta.json`
declares the sidebar order via a `pages` array. `mdx-components.tsx`
maps the Fumadocs renderers (code blocks, heading anchors, tables) onto every
page; Callout/Tabs/Steps/Cards are imported explicitly per page. A `<Step>`
takes no `title` prop — start it with a `###` heading instead.

Design: `app/global.css` holds the theme tokens (shared with the console:
charcoal surfaces, one signal-teal accent, Geist) and the docs chrome;
`app/home.css` styles the landing page, whose copy lives in
`lib/home-copy.ts` and must only restate what the docs say.
The parity script and the CI `docs` job keep the two locales
aligned.
