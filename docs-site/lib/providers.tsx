"use client";

import type { ReactNode } from "react";
import { useRouter } from "next/navigation";
import { RootProvider } from "fumadocs-ui/provider/next";

/** Client wrapper: the locale switcher's callback must live on the client. */
export function Providers({ locale, children }: { locale: string; children: ReactNode }) {
  const router = useRouter();
  return (
    <RootProvider
      i18n={{
        locale,
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
