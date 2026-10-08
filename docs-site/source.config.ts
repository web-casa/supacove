import { defineConfig, defineDocs } from "fumadocs-mdx/config";

export const docs = defineDocs({
  dir: "content/docs",
  docs: {
    // Last git commit time per file, exposed as page.data.lastModified. Needs
    // full history at build time (fetch-depth: 0 in CI); undefined otherwise.
    lastModified: true,
  },
});

export default defineConfig({
  mdxOptions: {
    // keep remark/rehype defaults; docs are plain MDX
  },
});
