import { absoluteTime, relativeTime } from "../../lib/format";

/** "2h ago", with the absolute timestamp on hover / focus. */
export function RelativeTime({ at }: { at: number }) {
  return (
    <time
      className="reltime num"
      dateTime={new Date(at * 1000).toISOString()}
      data-tip={absoluteTime(at)}
      tabIndex={0}
    >
      {relativeTime(at)}
    </time>
  );
}
