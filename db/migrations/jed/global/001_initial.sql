CREATE TABLE records (
 family_id text NOT NULL, kind text NOT NULL, id text NOT NULL, data text NOT NULL,
 PRIMARY KEY(family_id,kind,id)
);
---- create above / drop below ----
DROP TABLE records;
