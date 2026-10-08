import type { BaseLayoutProps } from "fumadocs-ui/layouts/shared";
import { Brand } from "@/components/logo";
import { localePath } from "@/lib/site";

const STRINGS = {
  zh: { tag: "文档" },
  en: { tag: "docs" },
} as const;

export function baseOptions(lang: string, opts: { tag?: boolean } = {}): BaseLayoutProps {
  const s = STRINGS[lang as keyof typeof STRINGS] ?? STRINGS.en;
  return {
    nav: {
      title: <Brand tag={opts.tag === false ? undefined : s.tag} />,
      url: localePath(lang),
    },
    links: [],
  };
}
