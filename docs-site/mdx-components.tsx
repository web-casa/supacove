import defaultMdxComponents from "fumadocs-ui/mdx";
import type { MDXComponents } from "mdx/types";

// The docs page passes this mapping to every MDX body, so fenced code,
// headings, links and tables get the Fumadocs renderers (copy button, heading
// anchors, scrollable tables). Callout/Tabs/Steps/Cards are still imported
// explicitly at the top of each .mdx.
export function getMDXComponents(components?: MDXComponents): MDXComponents {
  return { ...defaultMdxComponents, ...components };
}
