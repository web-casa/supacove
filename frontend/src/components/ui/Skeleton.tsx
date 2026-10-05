import { useI18n } from "../../i18n";

/** Placeholder rows shaped like the tables they stand in for. */
export function SkeletonRows({ rows = 3 }: { rows?: number }) {
  const { t } = useI18n();
  return (
    <div className="skeleton-rows" aria-busy="true" aria-label={t("ui.loading")}>
      {Array.from({ length: rows }, (_, i) => (
        <div className="skeleton-row" key={i}>
          <span className="skeleton skeleton-wide" />
          <span className="skeleton" />
          <span className="skeleton" />
          <span className="skeleton skeleton-short" />
        </div>
      ))}
    </div>
  );
}

export function SkeletonText({ short }: { short?: boolean }) {
  return <span className={`skeleton skeleton-inline${short ? " skeleton-short" : ""}`} />;
}
