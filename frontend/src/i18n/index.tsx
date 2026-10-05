// React bindings for i18n/core.ts: the provider re-renders the tree on
// language change and keeps <html lang> in sync via an effect (never during
// render). Only components are exported here — constants and helpers stay in
// core.ts so fast-refresh tooling sees one component module.
import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { getLang, setLang as persistLang, translate, type Lang, type Vars } from "./core";

interface I18n {
  lang: Lang;
  setLang: (l: Lang) => void;
  t: (key: string, vars?: Vars) => string;
}

const I18nContext = createContext<I18n | null>(null);

export function I18nProvider({ children }: { children: ReactNode }) {
  const [lang, setLangState] = useState<Lang>(getLang);

  useEffect(() => {
    document.documentElement.lang = lang;
  }, [lang]);

  const value = useMemo<I18n>(
    () => ({
      lang,
      setLang: (l: Lang) => {
        persistLang(l);
        setLangState(l);
      },
      t: (key, vars) => translate(lang, key, vars),
    }),
    [lang],
  );

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n(): I18n {
  const ctx = useContext(I18nContext);
  if (!ctx) throw new Error("useI18n must be used inside <I18nProvider>");
  return ctx;
}
