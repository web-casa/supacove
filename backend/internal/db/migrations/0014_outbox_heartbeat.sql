-- Phase 7: notification outbox (dev-plan P7-1) and per-database dead-man
-- switch configuration (P7-2). Includes the UPGRADE path for the Phase-4
-- webhook vocabulary (round-1 review P1-02/P2-04): legacy subscriptions
-- "failure,expired" are converted to the new event names, and legacy
-- duplicate live names are deterministically renamed before the unique
-- index lands.
-- +goose Up
CREATE TABLE notification_outbox (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id        TEXT    NOT NULL UNIQUE,
    event_type      TEXT    NOT NULL,
    database_id     INTEGER,
    database_name   TEXT    NOT NULL DEFAULT '',
    payload         TEXT    NOT NULL,
    state           TEXT    NOT NULL DEFAULT 'pending'
                    CHECK (state IN ('pending','delivering','delivered','dead')),
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at INTEGER NOT NULL,
    last_error      TEXT    NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    delivered_at    INTEGER
);
CREATE INDEX idx_outbox_due ON notification_outbox(state, next_attempt_at);

-- Legacy subscription vocabulary (0009 default "failure,expired") must keep
-- delivering after the upgrade: exact new-vocabulary matching would silently
-- zero every target and the empty-target path would mark events delivered.
UPDATE webhooks
SET events = REPLACE(REPLACE(events, 'failure', 'backup_failed'), 'expired', 'backup_expired')
WHERE deleted_at IS NULL
  AND (events LIKE '%failure%' OR events LIKE '%expired%');

-- Legacy live rows may share a name (0009 had no constraint). Keep the
-- oldest, deterministically rename the rest — never delete a receiver.
UPDATE webhooks
SET name = name || ' #' || id
WHERE deleted_at IS NULL
  AND id NOT IN (SELECT MIN(id) FROM webhooks WHERE deleted_at IS NULL GROUP BY name)
  AND EXISTS (
    SELECT 1 FROM webhooks w2
    WHERE w2.name = webhooks.name AND w2.deleted_at IS NULL AND w2.id < webhooks.id
  );

-- Webhook names are unique among LIVE rows (soft-deleted names free up).
CREATE UNIQUE INDEX idx_webhooks_live_name ON webhooks(name) WHERE deleted_at IS NULL;

ALTER TABLE databases ADD COLUMN heartbeat_url TEXT NOT NULL DEFAULT '';
ALTER TABLE databases ADD COLUMN heartbeat_period_hours INTEGER NOT NULL DEFAULT 0;
ALTER TABLE databases ADD COLUMN heartbeat_grace_hours INTEGER NOT NULL DEFAULT 0;
ALTER TABLE databases ADD COLUMN last_heartbeat_at INTEGER NOT NULL DEFAULT 0;

-- +goose Down
DROP INDEX IF EXISTS idx_webhooks_live_name;
ALTER TABLE databases DROP COLUMN last_heartbeat_at;
ALTER TABLE databases DROP COLUMN heartbeat_grace_hours;
ALTER TABLE databases DROP COLUMN heartbeat_period_hours;
ALTER TABLE databases DROP COLUMN heartbeat_url;
DROP TABLE notification_outbox;
-- NOTE: the legacy-vocabulary conversion and duplicate renames are NOT
-- reversed by Down (reverting would reintroduce values the new code cannot
-- match); a rollback lands on the old binary, which treats the converted
-- vocabulary as unknown-but-harmless CSV.
