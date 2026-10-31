CREATE TABLE entries (
  id serial PRIMARY KEY,
  booked_on date NOT NULL,
  memo text NOT NULL,
  cents bigint NOT NULL
);
INSERT INTO entries (booked_on, memo, cents) VALUES
  ('2026-01-05', 'Opening balance', 100000),
  ('2026-01-09', 'Hosting', -1250),
  ('2026-01-15', 'Invoice 1001', 45000);
