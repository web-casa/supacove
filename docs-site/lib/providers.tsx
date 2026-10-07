"use client";

import type { ReactNode } from "react";
import { useRouter } from "next/navigation";
import { RootProvider } from "fumadocs-ui/provider/next";
import { uiI18n } from "@/lib/ui-i18n";

/** Client wrapper: the locale switcher's callback must live on the client. */
export function Providers({ locale, children }: { locale: "zh" | "en"; children: ReactNode }) {
  const router = useRouter();
  return (
    <RootProvider
      i18n={{
        ...uiI18n.provider(locale),
        onLocaleChange: (v) => {
          router.replace(window.location.pathname.replace(new RegExp(`^/${locale}(/|$)`), `/${v}$1`));
        },
      }}
      search={{ options: { api: "/api/search" } }}
    >
      {children}
    </RootProvider>
  );
}
