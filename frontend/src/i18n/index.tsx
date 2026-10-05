// Minimal i18n: flat string dictionaries, a React context for re-rendering,
// and a module-level mirror so non-React helpers (lib/format.ts) can read the
// active language. Pluralisation follows the ICU-lite convention
// `key.one` / `key.other`, selected by a `count` variable.
import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from "react";
import { en, type Dict } from "./en";
import { zhCN } from "./zh-CN";

export type Lang = "en" | "zh-CN";

export const LANGS: { id: Lang; label: string }[] = [
  { id: "en", label: "EN" },
  { id: "zh-CN", label: "中文" },
];

const STORAGE_KEY = "sb.lang";
const DICTS: Record<Lang, Dict> = { en, "zh-CN": zhCN };

export type Vars = Record<string, string | number>;

/** Reads the active language without a React subscription. */
let currentLang: Lang = detect();

function detect(): Lang {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored === "en" || stored === "zh-CN") return stored;
  } catch {
    // private mode: fall through to the navigator
  }
  return typeof navigator !== "undefined" && navigator.language?.toLowerCase().startsWith("zh") ? "zh-CN" : "en";
}

export function getLang(): Lang {
  return currentLang;
}

export function translate(lang: Lang, key: string, vars?: Vars): string {
  const dict = DICTS[lang] as Record<string, string>;
  let template: string | undefined = dict[key];
  if (template === undefined && vars && "count" in vars && vars.count === 1) template = dict[`${key}.one`];
  if (template === undefined) template = vars && "count" in vars ? dict[`${key}.other`] ?? dict[key] : undefined;
  if (template === undefined) template = key; // unknown keys (raw API states) pass through
  if (!vars) return template;
  return template.replace(/\{(\w+)\}/g, (m, k: string) => (k in vars ? String(vars[k]) : m));
}

interface I18n {
  lang: Lang;
  setLang: (l: Lang) => void;
  t: (key: string, vars?: Vars) => string;
}

const I18nContext = createContext<I18n | null>(null);

export function I18nProvider({ children }: { children: ReactNode }) {
  const [lang, setLangState] = useState<Lang>(currentLang);

  const setLang = useCallback((l: Lang) => {
    setLangState(l);
    currentLang = l;
    try {
      localStorage.setItem(STORAGE_KEY, l);
    } catch {
      // private mode: the choice lives for this tab only
    }
  }, []);

  const value = useMemo<I18n>(() => {
    document.documentElement.lang = lang;
    return { lang, setLang, t: (key, vars) => translate(lang, key, vars) };
  }, [lang, setLang]);

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n(): I18n {
  const ctx = useContext(I18nContext);
  if (!ctx) throw new Error("useI18n must be used inside <I18nProvider>");
  return ctx;
}
