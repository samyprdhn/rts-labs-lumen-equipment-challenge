-- Lumen Builders equipment scheduling
-- Initial schema, created by the previous contractor as a direct copy of the
-- spreadsheet tabs so the office could "get off Excel quickly".
-- SQLite dialect. Loaded by scripts/seed.mjs.

DROP TABLE IF EXISTS sites;
DROP TABLE IF EXISTS equipment;
DROP TABLE IF EXISTS operators;
DROP TABLE IF EXISTS bookings;
DROP TABLE IF EXISTS users;

CREATE TABLE sites (
  site_id        TEXT,
  name           TEXT,
  city           TEXT,
  state          TEXT,
  timezone       TEXT,
  region         TEXT,
  superintendent TEXT
);

CREATE TABLE equipment (
  asset_tag      TEXT,
  type           TEXT,
  make_model     TEXT,
  capacity       TEXT,
  home_yard      TEXT,
  daily_rate_usd TEXT,
  required_cert  TEXT,
  status         TEXT,
  notes          TEXT
);

CREATE TABLE operators (
  operator_id       TEXT,
  first_name        TEXT,
  last_name         TEXT,
  phone             TEXT,
  email             TEXT,
  date_of_birth     TEXT,
  ssn_last4         TEXT,
  emergency_contact TEXT,
  union_local       TEXT,
  certifications    TEXT,
  home_region       TEXT
);

CREATE TABLE bookings (
  booking_id  TEXT,
  asset_tag   TEXT,
  site_id     TEXT,
  start_at    TEXT,
  end_at      TEXT,
  operator_id TEXT,
  booked_by   TEXT,
  status      TEXT,
  notes       TEXT,
  -- Added for Part 2 (docs/decisions/0002-reservation-concurrency-control.md).
  -- Additive, nullable: the original start_at/end_at columns are untouched
  -- and keep their 4 mixed raw formats (see Part 1 recon). start_at_utc/
  -- end_at_utc hold a canonical, strictly-sortable ISO-8601 UTC string -
  -- the trigger below compares these, never the raw columns, because a raw
  -- SQL string comparison across 4 incompatible formats would silently
  -- reproduce the exact bug Part 1 fixed in application code. New rows
  -- (created via POST /api/reservations) always populate these at insert
  -- time. The 743 historical rows get them via a one-time backfill
  -- (scripts/backfill_booking_utc.go) that reuses Part 1's already-proven
  -- multi-format parser - NOT by this schema/seed script, since seed.mjs
  -- loads the CSV columns verbatim and doesn't know these columns exist.
  -- expires_at_utc is set only for a tentative hold (status='tentative');
  -- NULL means "no expiry set" (confirmed bookings) or "not yet backfilled".
  start_at_utc   TEXT,
  end_at_utc     TEXT,
  expires_at_utc TEXT
);

-- Enforces: no two active (confirmed, or tentative-and-not-yet-expired)
-- bookings for the same asset may have overlapping [start_at_utc, end_at_utc)
-- ranges. This is the schema-level backstop for the concurrency guarantee in
-- docs/decisions/0002-reservation-concurrency-control.md - empirically
-- verified there (10/10 concurrent trials) to hold even for a bare, naive
-- INSERT with no application-level check at all.
--
-- Rows with NULL start_at_utc/end_at_utc (not yet backfilled, e.g. freshly
-- seeded historical data before the backfill runs) never match the overlap
-- comparison - SQL NULL comparisons are never true - so this trigger is
-- inert for bulk-seeded historical rows until they're backfilled, and never
-- rejects a seed load. It protects every row written through
-- POST /api/reservations going forward, which always populates these
-- columns at insert time.
--
-- expires_at_utc is wrapped in datetime(...) before comparing against
-- datetime('now') - comparing the raw TEXT column directly is wrong
-- whenever both share the same calendar day: our own canonical format is
-- "...T...Z" but datetime('now') returns "... ..." (space-separated), so
-- the 'T' character (0x54) always lexicographically outranks the space
-- (0x20) at that position, making an expiry from EARLIER TODAY compare as
-- "greater than now" (i.e. "not yet expired") regardless of the actual
-- time values that follow. datetime(...) re-parses both sides into
-- SQLite's own canonical format first, making the comparison correct.
-- Found via a real test failure that only manifested hours into a long
-- session, once "1 hour ago" stopped crossing a UTC midnight boundary by
-- accident - not caught by inspection, only by the comparison actually
-- being exercised at the right (or rather wrong) time of day.
CREATE TRIGGER no_overlapping_active_bookings
BEFORE INSERT ON bookings
WHEN NEW.status IN ('confirmed', 'tentative')
BEGIN
  SELECT RAISE(ABORT, 'overlapping active booking exists')
  WHERE EXISTS (
    SELECT 1 FROM bookings
    WHERE asset_tag = NEW.asset_tag
      AND (
        status = 'confirmed'
        OR (status = 'tentative' AND (expires_at_utc IS NULL OR datetime(expires_at_utc) > datetime('now')))
      )
      AND NEW.start_at_utc < end_at_utc
      AND start_at_utc < NEW.end_at_utc
  );
END;

CREATE TABLE users (
  user_id  TEXT,
  name     TEXT,
  email    TEXT,
  role     TEXT,
  site_ids TEXT
);
