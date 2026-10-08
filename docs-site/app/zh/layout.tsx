import type { ReactNode } from "react";
import { LocaleLayout, localeMetadata, viewport } from "@/routes/locale-layout";

export const metadata = localeMetadata("zh");
export { viewport };

export default function Layout({ children }: { children: ReactNode }) {
  return <LocaleLayout lang="zh">{children}</LocaleLayout>;
}
