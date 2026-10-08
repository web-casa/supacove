import { createMDX } from "fumadocs-mdx/next";

export default createMDX()({
  reactStrictMode: true,
  experimental: {
    // One static 404 document for all unmatched URLs: app/global-not-found.tsx.
    globalNotFound: true,
  },
});
