# Operating Money Bags

Money Bags runs one active Go process behind HTTPS. Runtime settings are:

| Variable | Purpose / default |
| --- | --- |
| `PORT`, `BIND_ADDRESS` | Listen port (4000) and address (`127.0.0.1`); Verna supplies `PORT` |
| `DATABASE_BACKEND` | `postgresql` (default) or `jed` |
| `DATABASE_URL` | PostgreSQL connection URL; required for that backend |
| `JED_DATA_DIR` | Jed directory, default `data/jed` |
| `ATTACHMENTS_DIR` | Private attachment directory, default `data/attachments` |
| `ASSETS_DIR` | Static assets, default `build/assets`, falling back to packaged `assets` |
| `MCP_CANONICAL_URL` | Public origin, default `http://localhost:$PORT` |
| `WEBAUTHN_RP_ID` | Public hostname, default hostname of canonical origin |
| `WEBAUTHN_ORIGIN` | Must match the canonical origin |
| `SECURE_COOKIES` | Defaults true for HTTPS; required for public deployment |
| `ALLOW_REGISTRATION` | Defaults false; set true to allow new accounts and families |
| `FAMILY_STORAGE_QUOTA_BYTES` | Default `1073741824` (1 GiB per family) |
| `LOG_FORMAT` | `json` (default), `text`, or `journal` for direct systemd journald logging |
| `LOG_LEVEL` | `debug`, `info` (default), `warn`, or `error` |

Registration can also be enabled with `moneybags server --allow-registration`.
An explicit `--allow-registration=false` overrides an enabled environment
setting. Restart the server to apply changes. To create the first family,
temporarily enable registration, create an account, then disable it and restart.
Existing users can sign in and invite family members while registration is
disabled. The public `GET /api/settings` endpoint reports `allow_registration`
so the login screen only offers signup when enabled; the backend also enforces it.

Logging settings are case-insensitive; empty or unrecognized values fall back
to JSON and info. JSON and text logs go to stderr, leaving administrative action
results on stdout. `LOG_FORMAT=journal` uses the same `slog-journal` handler as
Logger4Life and FAM, sending native severity and structured fields directly to
`/run/systemd/journal/socket`. Attribute and group names are normalized to
uppercase journal field names (for example, `http.request.method` becomes
`HTTP_REQUEST_METHOD`). Use journal format on hosts with journald available;
the handler silently drops messages when the journal socket is absent.

Production requires HTTPS, persistent data directories and a restorable backup.
Serve attachment bytes only through the authenticated backend. Do not configure
Caddy to serve the attachment or Jed directories. The application imposes body,
file, notes and rate limits; configure a corresponding reverse-proxy upload cap
of at least 30 MiB for the JSON/MCP envelope. Store filesystem data on a trusted,
local filesystem whose locking and durability semantics Jed supports.

## Migrations and permissions

Run `moneybags migrate` with the release executable and a migration-capable
PostgreSQL connection before starting the new release. The packaged migrations
also work with tern and `db/tern.conf`. Jed migrations are embedded and run when
opening a database file. Never change Jed file formats while an incompatible
process is running.

Provision a dedicated PostgreSQL database and migration owner; do not run the
service as a cluster superuser. Application queries use transaction-local
`app.family_id` and checked tenant views. The runtime role needs
`SELECT, INSERT, UPDATE, DELETE` on `records`, `bags`, `entries`, and `attachments`,
plus schema usage and database connect, while the migration owner owns the
underlying `all_*` tables and views. Avoid granting the runtime role direct
access to those tables. Administrative cross-family reconciliation needs the
migration/maintenance connection. Keep all SQL credentials outside archives.

## Cleanup and reconciliation

Administrative actions are intentionally absent from HTTP and MCP catalogs:

```sh
moneybags action cleanup_attachments '{"family_id":"FAMILY_ID","grace_seconds":3600}'
moneybags action cleanup_auth '{}'
moneybags action reconcile_auth '{}'
```

Cleanup removes expired unlinked uploads and unreachable blobs after the grace
period. Detached blobs are deleted after their database transaction commits;
cleanup recovers files left by interrupted writes or failed deletions. Entry
history retains file metadata, not historical bytes.

Reconciliation removes stale global projections after authoritative account
deletion and reports pending registrations, unreferenced or unreachable family
files. Review those reports and backups before recovery. It deliberately never
activates an orphan account or assigns a conflicting username automatically.
Deleted accounts immediately lose access even when global cleanup is interrupted.

## Backups and restores

The supplied scripts require Python 3.12+ and PostgreSQL client tools when using
PostgreSQL. They contain no credentials and never overwrite an existing backup
or nonempty restore destination.

1. Stop **all** Money Bags processes, including both Verna slots if a deployment
   is in progress, and stop scheduled cleanup. Stopping writes is required to
   keep the database and attachment snapshots coherent. For Jed, all database
   handles must be closed before copying.
2. With the application's database/directory environment loaded, run:

   ```sh
   MONEYBAGS_WRITES_STOPPED=1 scripts/backup /safe/location/moneybags.tar.gz
   ```

3. Resume the application after the backup completes. Store archives securely:
   they contain family data, credential hashes and receipt files.
4. To restore, stop the target application. Select the same backend and empty
   attachment/Jed directories or an empty, already-created PostgreSQL database:

   ```sh
   MONEYBAGS_WRITES_STOPPED=1 scripts/restore /safe/location/moneybags.tar.gz
   ```

5. Start a compatible release and verify login, bag balances, entry history and
   attachment downloads before allowing new writes.

`MONEYBAGS_WRITES_STOPPED=1` is the operator's assertion that all writers are
stopped; the scripts cannot stop a remote deployment for you. Restore validates
archive paths and rejects links. PostgreSQL restore runs in one transaction.

## Release verification

Local automated checks cover both databases, concurrent financial writes,
cross-process Jed overlap, OAuth/HTTP/MCP parity, virtual and software passkey
authenticators, attachment isolation and exact cent arithmetic. Cross compilation
does not test executable startup on foreign operating systems.

Before a production launch, connect the actual MCP client and perform a balance
question and receipt-photo write, verifying stored bytes and returned balance.
Run Verna deploy/rollback against a prepared test host, validate its HTTPS origin,
passkeys and persistent paths, and rehearse a complete backup restore. Those
external-client and host-specific checks require their configured environments;
SDK protocol tests alone do not establish them.
