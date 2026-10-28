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
  notes       TEXT
);

CREATE TABLE users (
  user_id  TEXT,
  name     TEXT,
  email    TEXT,
  role     TEXT,
  site_ids TEXT
);
