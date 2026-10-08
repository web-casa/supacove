import { createMDX } from "fumadocs-mdx/next";

export default createMDX()({
  // Pure static export for Cloudflare Pages: every route is prerendered into
  // out/. This means NO server runtime — middleware (the former proxy.ts)
  // does not run; /en/* canonicalization lives in public/_redirects and
  // response headers in public/_headers. The search index is baked at build
  // time (app/api/search) and queried client-side (orama-static).
  output: "export",
  reactStrictMode: true,
  experimental: {
    // One static 404 document for all unmatched URLs: app/global-not-found.tsx.
    globalNotFound: true,
  },
});
