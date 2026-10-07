import type { ReactNode } from "react";
import { notFound } from "next/navigation";
import { i18n } from "@/lib/i18n";
import { Providers } from "@/lib/providers";

export default async function LangLayout({
  children,
  params,
}: {
  children: ReactNode;
  params: Promise<{ lang: string }>;
}) {
  const { lang } = await params;
  if (!(i18n.languages as string[]).includes(lang)) notFound();
  return (
    <div lang={lang}>
      <Providers locale={lang}>{children}</Providers>
    </div>
  );
}
