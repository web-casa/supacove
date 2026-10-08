# SupaCove documentation site

Fumadocs (Next.js) user documentation for SupaCove (https://supacove.com),
bilingual. English is the default and is served unprefixed (`/`, `/docs/…`)
from `app/(en)`; Chinese lives under `/zh` in `app/zh`. Both render the
shared implementations in `routes/`. `proxy.ts` redirects `/en/…` to the
unprefixed URL and sends Chinese browsers from `/` to `/zh`.

The product was first called supabackup: the binary, image, metric names and
`SB_*` variables still use that name, and the docs show them as they are.

```bash
npm ci
npm run dev        # http://localhost:3000
npm run build      # parity check + static build
npm run check-parity   # both locales must ship the same slug set
npm run smoke      # after a build: status codes, redirects, 404, share image
```

Two things the build alone does not prove, both covered by
`scripts/smoke.mjs` (also run in CI):

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
