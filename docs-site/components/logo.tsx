/** The SupaCove mark: the same three-ring cylinder as the console favicon. */
export function Mark({ size = 20 }: { size?: number }) {
  return (
    <svg
      className="sb-mark"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      aria-hidden
    >
      <ellipse cx="12" cy="6.5" rx="7.5" ry="3" />
      <path d="M4.5 6.5v11c0 1.7 3.4 3 7.5 3s7.5-1.3 7.5-3v-11" />
      <path className="sb-mark-ring" d="M4.5 12c0 1.7 3.4 3 7.5 3s7.5-1.3 7.5-3" />
    </svg>
  );
}

export function Brand({ tag }: { tag?: string }) {
  return (
    <span className="sb-brand">
      <Mark />
      <span>SupaCove</span>
      {tag ? <span className="sb-brand-tag">{tag}</span> : null}
    </span>
  );
}
