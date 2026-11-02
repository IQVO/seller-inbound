-- The Idempotency-Key is GLOBAL (REST guideline 2.3): one stored record per key,
-- compared by fingerprint (method + path in `route`, plus the body hash). The
-- same key on another route is therefore a reuse (409), not a new key.
-- Additive for existing rows: a key already stored under two routes keeps only
-- its earliest row, then the primary key narrows from (route, key) to (key).
DELETE FROM idempotency_keys a
 USING idempotency_keys b
 WHERE a.key = b.key
   AND (a.created_at, a.route) > (b.created_at, b.route);

ALTER TABLE idempotency_keys DROP CONSTRAINT idempotency_keys_pkey;
ALTER TABLE idempotency_keys ADD PRIMARY KEY (key);
