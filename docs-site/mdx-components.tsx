import type { MDXComponents } from "mdx/types";

// Pages import their UI components (Callout/Tabs/Steps/Cards) explicitly at
// the top of each .mdx — that is the wiring in use today. This file exists so
// a future global mapping has a single home; it is intentionally minimal and
// NOT claimed to be auto-applied by the MDX runtime.
export function getMDXComponents(components?: MDXComponents): MDXComponents {
  return { ...components };
}
