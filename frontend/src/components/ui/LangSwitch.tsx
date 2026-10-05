import { LANGS, useI18n } from "../../i18n";

/** Compact EN / 中文 segmented toggle. */
export function LangSwitch() {
  const { lang, setLang, t } = useI18n();
  return (
    <div className="lang-switch" role="group" aria-label={t("lang.switch")}>
      {LANGS.map((l) => (
        <button
          type="button"
          key={l.id}
          className={`lang-opt${lang === l.id ? " lang-active" : ""}`}
          aria-pressed={lang === l.id}
          onClick={() => setLang(l.id)}
        >
          {l.label}
        </button>
      ))}
    </div>
  );
}
