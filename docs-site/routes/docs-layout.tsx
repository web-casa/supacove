import type { ReactNode } from "react";
import { DocsLayout } from "fumadocs-ui/layouts/docs";
import { baseOptions } from "@/app/layout.config";
import type { Lang } from "@/lib/i18n";
import { source } from "@/lib/source";

export function DocsShell({ lang, children }: { lang: Lang; children: ReactNode }) {
  return (
    <DocsLayout tree={source.pageTree[lang]} {...baseOptions(lang)}>
      {children}
    </DocsLayout>
  );
}
