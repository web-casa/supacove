import type { Metadata } from "next";
import { i18n, type Lang } from "@/lib/i18n";

/** Canonical origin: HTTPS, no www. Every absolute URL is built from this. */
export const SITE_URL = "https://supacove.com";
export const SITE_NAME = "SupaCove";

/** The source repository: navbar link, hero action and JSON-LD sameAs. */
export const GITHUB_URL = "https://github.com/web-casa/supacove";

/**
 * The share image served by app/opengraph-image/route.tsx. Pages list it explicitly: a
 * page-level `openGraph` object replaces the inherited one, image included.
 */
export const OG_IMAGE = {
  url: "/opengraph-image",
  width: 1200,
  height: 630,
  alt: "SupaCove — self-hosted, encrypted PostgreSQL backups",
};

/**
 * Public path of a locale-independent path ("" for the home page, "/docs/x"
 * for a doc): unprefixed in English, under /zh in Chinese.
 */
export function localePath(lang: string, path = ""): string {
  if (lang === i18n.defaultLanguage) return path || "/";
  return `/${lang}${path}`;
}

export function absoluteUrl(lang: string, path = ""): string {
  const p = localePath(lang, path);
  return p === "/" ? SITE_URL : `${SITE_URL}${p}`;
}

/**
 * Self-referencing canonical plus the reciprocal hreflang set for one page.
 * x-default points at the English equivalent of the same page.
 */
export function alternates(lang: string, path = ""): NonNullable<Metadata["alternates"]> {
  return {
    canonical: absoluteUrl(lang, path),
    languages: {
      ...Object.fromEntries(i18n.languages.map((l: Lang) => [l, absoluteUrl(l, path)])),
      "x-default": absoluteUrl(i18n.defaultLanguage, path),
    },
  };
}
