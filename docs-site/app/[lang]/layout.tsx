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
  const { lang } = (await params) as { lang: "zh" | "en" };
  if (!(i18n.languages as string[]).includes(lang)) notFound();
  return (
    // <html> belongs to the root layout, but the language attribute must
    // follow the route: Next hoists this <html> into the document head for
    // the segment (the root <html> carries no lang of its own).
    <html lang={lang} suppressHydrationWarning>
      <body className="flex flex-col min-h-screen">
        <Providers locale={lang}>{children}</Providers>
      </body>
    </html>
  );
}
