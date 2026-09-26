-- All tenant access uses transaction-local app.family_id and checked views.
CREATE TABLE IF NOT EXISTS all_records (
 family_id text NOT NULL, kind text NOT NULL, id text NOT NULL, data text NOT NULL,
 PRIMARY KEY(family_id,kind,id)
);
CREATE TABLE IF NOT EXISTS all_bags (
 family_id text NOT NULL, id text NOT NULL, normalized_name text NOT NULL, archived boolean NOT NULL, data text NOT NULL,
 PRIMARY KEY(family_id,id), UNIQUE(family_id,normalized_name)
);
CREATE TABLE IF NOT EXISTS all_entries (
 family_id text NOT NULL, id text NOT NULL, bag_id text NOT NULL,
 amount_cents bigint NOT NULL CHECK(amount_cents BETWEEN -9007199254740991 AND 9007199254740991),
 entry_date text NOT NULL, created_at text NOT NULL, data text NOT NULL,
 PRIMARY KEY(family_id,id), FOREIGN KEY(family_id,bag_id) REFERENCES all_bags(family_id,id)
);
CREATE INDEX IF NOT EXISTS all_entries_bag_date ON all_entries(family_id,bag_id,entry_date DESC,created_at DESC,id DESC);
CREATE TABLE IF NOT EXISTS all_attachments (
 family_id text NOT NULL, id text NOT NULL, entry_id text NOT NULL, data text NOT NULL,
 PRIMARY KEY(family_id,id), FOREIGN KEY(family_id,entry_id) REFERENCES all_entries(family_id,id)
);
CREATE OR REPLACE VIEW records AS SELECT * FROM all_records WHERE family_id=current_setting('app.family_id',true) WITH CASCADED CHECK OPTION;
CREATE OR REPLACE VIEW bags AS SELECT * FROM all_bags WHERE family_id=current_setting('app.family_id',true) WITH CASCADED CHECK OPTION;
CREATE OR REPLACE VIEW entries AS SELECT * FROM all_entries WHERE family_id=current_setting('app.family_id',true) WITH CASCADED CHECK OPTION;
CREATE OR REPLACE VIEW attachments AS SELECT * FROM all_attachments WHERE family_id=current_setting('app.family_id',true) WITH CASCADED CHECK OPTION;
---- create above / drop below ----
DROP VIEW attachments, entries, bags, records;
DROP TABLE all_attachments, all_entries, all_bags, all_records;
