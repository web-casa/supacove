"use client";

import type { ReactNode } from "react";
import { useRouter } from "next/navigation";
import { RootProvider } from "fumadocs-ui/provider/next";
import { localePath } from "@/lib/site";
import { uiI18n } from "@/lib/ui-i18n";

/** Client wrapper: the locale switcher's callback must live on the client. */
export function Providers({ locale, children }: { locale: "zh" | "en"; children: ReactNode }) {
  const router = useRouter();
  return (
    <RootProvider
      i18n={{
        ...uiI18n.provider(locale),
        onLocaleChange: (v) => {
          // Remember an explicit choice so proxy.ts stops negotiating at "/".
          document.cookie = `sc_lang=${v}; path=/; max-age=31536000; samesite=lax`;
          const path = window.location.pathname.replace(/^\/zh(?=\/|$)/, "");
          router.replace(localePath(v, path === "/" ? "" : path));
        },
      }}
      search={{ options: { api: "/api/search" } }}
    >
      {children}
    </RootProvider>
  );
}
