// The landing page's signature piece: rows of pg_dump output drift through an
// "age" gate and come out the other side as ciphertext. Two identical-width
// monospace tracks (plain / cipher) are clipped at the gate and animated with
// one shared transform, so each glyph appears to be encrypted as it crosses.
// Pure CSS — see .lp-stream in app/home.css.

const SQL = [
  "COPY public.orders (id, customer_id, total_cents, placed_at) FROM stdin;",
  "48213  1907  12940  2026-03-14 09:26:53+00",
  "CREATE INDEX orders_placed_at_idx ON public.orders USING btree (placed_at);",
  "COPY public.customers (id, email, plan, created_at) FROM stdin;",
  "1907  mira.okafor@example.org  team  2025-11-02 17:41:08+00",
  "ALTER TABLE ONLY public.invoices ADD CONSTRAINT invoices_pkey PRIMARY KEY (id);",
  "CREATE TABLE public.sessions (token bytea NOT NULL, user_id bigint, expires_at timestamptz);",
  "SELECT pg_catalog.setval('public.orders_id_seq', 48213, true);",
  "48214  2231  4675  2026-03-14 09:27:11+00",
  "ALTER TABLE ONLY public.orders ADD CONSTRAINT orders_customer_id_fkey FOREIGN KEY (customer_id) REFERENCES public.customers(id);",
  "COPY public.invoices (id, order_id, issued_at, paid) FROM stdin;",
  "9031  48213  2026-03-14 09:30:02+00  t",
];

const B64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

// Deterministic PRNG: the ciphertext must be identical on server and client.
function mulberry32(seed: number) {
  return () => {
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const ROWS = Array.from({ length: 6 }, (_, row) => {
  let plain = "";
  for (let i = 0; plain.length < 520; i++) plain += SQL[(row * 5 + i * 7) % SQL.length] + "    ";
  const rand = mulberry32(row + 1);
  let cipher = "";
  for (let i = 0; i < plain.length; i++) cipher += B64[Math.floor(rand() * 64)];
  return { plain, cipher, duration: 190 + ((row * 37) % 70) };
});

export function CipherStream({ labels }: { labels: { plain: string; cipher: string; gate: string } }) {
  return (
    <div className="lp-stream sx-stream" aria-hidden>
      <div className="lp-stream-rows">
        {ROWS.map((r, i) => (
          <div className="lp-stream-row" key={i} style={{ animationDuration: `${r.duration}s` }}>
            <div className="lp-stream-layer lp-stream-plain">
              <span>{r.plain}</span>
              <span>{r.plain}</span>
            </div>
            <div className="lp-stream-layer lp-stream-cipher">
              <span>{r.cipher}</span>
              <span>{r.cipher}</span>
            </div>
          </div>
        ))}
      </div>
      <div className="lp-gate">
        <span className="lp-gate-chip">{labels.gate}</span>
      </div>
      <div className="lp-stream-legend">
        <span>
          <i /> pg_dump · {labels.plain}
        </span>
        <span>
          {labels.cipher} · backups/&lt;uuid&gt;.dump.age <i />
        </span>
      </div>
    </div>
  );
}
