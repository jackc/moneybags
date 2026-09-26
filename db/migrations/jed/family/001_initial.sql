CREATE TABLE records (
 family_id text NOT NULL, kind text NOT NULL, id text NOT NULL, data text NOT NULL,
 PRIMARY KEY(family_id,kind,id)
);
CREATE TABLE bags (
 family_id text NOT NULL, id text NOT NULL, normalized_name text NOT NULL, archived boolean NOT NULL, data text NOT NULL,
 PRIMARY KEY(family_id,id), UNIQUE(family_id,normalized_name)
);
CREATE TABLE entries (
 family_id text NOT NULL, id text NOT NULL, bag_id text NOT NULL,
 amount_cents bigint NOT NULL CHECK(amount_cents BETWEEN -9007199254740991 AND 9007199254740991),
 entry_date text NOT NULL, created_at text NOT NULL, data text NOT NULL,
 PRIMARY KEY(family_id,id), FOREIGN KEY(family_id,bag_id) REFERENCES bags(family_id,id)
);
CREATE INDEX entries_bag_date ON entries(family_id,bag_id,entry_date,created_at,id);
CREATE TABLE attachments (
 family_id text NOT NULL, id text NOT NULL, entry_id text NOT NULL, data text NOT NULL,
 PRIMARY KEY(family_id,id), FOREIGN KEY(family_id,entry_id) REFERENCES entries(family_id,id)
);
---- create above / drop below ----
DROP TABLE attachments;
DROP TABLE entries;
DROP TABLE bags;
DROP TABLE records;
