import type { ReactNode } from "react";
import { DocsLayout } from "fumadocs-ui/layouts/docs";
import { GithubMark } from "@/components/github-mark";
import { baseOptions } from "@/app/layout.config";
import type { Lang } from "@/lib/i18n";
import { GITHUB_URL } from "@/lib/site";
import { source } from "@/lib/source";

/** Sidebar footer: the GitHub entrance on every docs page. */
function SidebarGithub() {
  return (
    <a
      className="sb-sidebar-github"
      href={GITHUB_URL}
      target="_blank"
      rel="noopener noreferrer"
    >
      <GithubMark size={15} />
      GitHub
    </a>
  );
}

export function DocsShell({ lang, children }: { lang: Lang; children: ReactNode }) {
  return (
    <DocsLayout
      tree={source.pageTree[lang]}
      {...baseOptions(lang)}
      sidebar={{ footer: <SidebarGithub /> }}
    >
      {children}
    </DocsLayout>
  );
}
