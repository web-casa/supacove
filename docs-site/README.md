# supabackup documentation site

Fumadocs (Next.js) user documentation, bilingual (zh default, en mirror).

```bash
npm ci
npm run dev        # http://localhost:3000
npm run build      # parity check + static build
npm run check-parity   # both locales must ship the same slug set
```

Content lives in `content/docs/{zh,en}/<slug>.mdx` with a `meta.json` per
locale defining sidebar titles. MDX components (Callout/Tabs/Steps/Cards) are
imported explicitly per page (Fumadocs v16 has no global component map).
The parity script and the CI `docs` job keep the two locales aligned.
