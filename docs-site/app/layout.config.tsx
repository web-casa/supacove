import type { BaseLayoutProps } from "fumadocs-ui/layouts/shared";
import { BookOpen } from "lucide-react";

const STRINGS = {
  zh: { name: "supabackup 文档" },
  en: { name: "supabackup docs" },
} as const;

export function baseOptions(lang: string): BaseLayoutProps {
  const s = STRINGS[lang as keyof typeof STRINGS] ?? STRINGS.en;
  return {
    nav: {
      title: (
        <>
          <BookOpen className="size-4" aria-hidden />
          <span>{s.name}</span>
        </>
      ),
    },
    links: [],
  };
}
