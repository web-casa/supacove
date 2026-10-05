/** Placeholder rows shaped like the tables they stand in for. */
export function SkeletonRows({ rows = 3 }: { rows?: number }) {
  return (
    <div className="skeleton-rows" aria-busy="true" aria-label="Loading">
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
