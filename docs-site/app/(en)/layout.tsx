import type { ReactNode } from "react";
import { LocaleLayout, localeMetadata, viewport } from "@/routes/locale-layout";

export const metadata = localeMetadata("en");
export { viewport };

export default function Layout({ children }: { children: ReactNode }) {
  return <LocaleLayout lang="en">{children}</LocaleLayout>;
}
