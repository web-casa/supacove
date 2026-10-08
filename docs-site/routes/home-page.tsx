import type { Metadata } from "next";
import { Home } from "@/components/home";
import { JsonLd } from "@/components/json-ld";
import type { Lang } from "@/lib/i18n";
import { SITE_META } from "@/lib/site-meta";
import { OG_IMAGE, SITE_NAME, SITE_URL, absoluteUrl, alternates } from "@/lib/site";

export function homeMetadata(lang: Lang): Metadata {
  const m = SITE_META[lang];
  return {
    title: { absolute: m.title },
    description: m.description,
    alternates: alternates(lang),
    openGraph: {
      type: "website",
      siteName: SITE_NAME,
      title: m.title,
      description: m.description,
      url: absoluteUrl(lang),
      locale: m.locale,
      images: [OG_IMAGE],
    },
    twitter: { card: "summary_large_image", title: m.title, description: m.description, images: [OG_IMAGE] },
  };
}

export function HomePage({ lang }: { lang: Lang }) {
  const m = SITE_META[lang];
  return (
    <>
      <JsonLd
        data={{
          "@context": "https://schema.org",
          "@graph": [
            { "@type": "WebSite", "@id": `${SITE_URL}/#website`, url: SITE_URL, name: SITE_NAME, inLanguage: ["en", "zh"] },
            {
              "@type": "SoftwareApplication",
              "@id": `${SITE_URL}/#software`,
              name: SITE_NAME,
              url: absoluteUrl(lang),
              description: m.description,
              inLanguage: lang,
              applicationCategory: "DeveloperApplication",
              // Ships as a Linux container image; see the installation page.
              operatingSystem: "Linux (Docker)",
              license: "https://www.gnu.org/licenses/agpl-3.0.html",
              // The software is free; compute, storage and egress are not.
              offers: { "@type": "Offer", price: 0, priceCurrency: "USD" },
            },
          ],
        }}
      />
      <Home lang={lang} />
    </>
  );
}
