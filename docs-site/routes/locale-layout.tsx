// Shared by app/(en)/layout.tsx and app/zh/layout.tsx. Each locale has its own
// root layout so that English can live at unprefixed URLs without rewrites.
import type { ReactNode } from "react";
import "@/app/global.css";
import type { Metadata, Viewport } from "next";
import { Geist, Geist_Mono } from "next/font/google";
import type { Lang } from "@/lib/i18n";
import { Providers } from "@/lib/providers";
import { SITE_META as META } from "@/lib/site-meta";
import { SITE_NAME, SITE_URL } from "@/lib/site";

// Same faces as the console. CJK falls through to the system stack declared
// in global.css (shipping a CJK webfont would cost megabytes per page).
const sans = Geist({ subsets: ["latin"], variable: "--font-geist-sans", display: "swap" });
const mono = Geist_Mono({ subsets: ["latin"], variable: "--font-geist-mono", display: "swap" });

/** Site-wide metadata defaults for one locale. */
export function localeMetadata(lang: Lang): Metadata {
  const m = META[lang];
  return {
    metadataBase: new URL(SITE_URL),
    // Pages set their own title, description, canonical and Open Graph
    // fields (metadata merges shallowly, so nothing page-specific lives here).
    title: { default: m.title, template: `%s · ${SITE_NAME}` },
    description: m.description,
    applicationName: SITE_NAME,
  };
}

export const viewport: Viewport = {
  themeColor: [
    { media: "(prefers-color-scheme: dark)", color: "#101317" },
    { media: "(prefers-color-scheme: light)", color: "#f4f6f7" },
  ],
};

/** The document shell for one locale: <html lang>, fonts, skip link, providers. */
export function LocaleLayout({ lang, children }: { lang: Lang; children: ReactNode }) {
  return (
    <html lang={lang} className={`${sans.variable} ${mono.variable}`} suppressHydrationWarning>
      <body className="flex flex-col min-h-screen">
        <a className="sb-skip" href="#nd-page">
          {META[lang].skip}
        </a>
        <Providers locale={lang}>{children}</Providers>
      </body>
    </html>
  );
}
