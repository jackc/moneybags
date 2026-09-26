# Money Bags build and deployment

Status: implemented build contract and operator guide. The application, mise
tasks, release script and deployment template are present. Live Verna rollout
checks require a prepared test host; see [operations](../../docs/operations.md).

## Build tasks

Follow [FAM's mise configuration](../../../fam/mise.toml) and
[Logger4Life's mise configuration](../../../logger4life/.mise.toml). Pin Go,
Node, mise-managed development tools, and Verna to tested versions when the
project is scaffolded. Keep machine-specific settings in ignored
`mise.local.toml`.

| Task | Behavior and output |
| --- | --- |
| `mise run build:binary` | Build the native Go executable at `build/moneybags` |
| `mise run build:assets` | Run the SvelteKit static build into `build/assets/`; write `build/assets/.built` only after success |
| `mise run build` | Run the binary and asset builds, concurrently where possible |
| `mise run build:linux-amd64` | Build `build/linux_amd64/` and `build/linux_amd64.tar.gz` |
| `mise run build:linux-arm64` | Build `build/linux_arm64/` and `build/linux_arm64.tar.gz` |
| `mise run build:darwin-amd64` | Build `build/darwin_amd64/` and `build/darwin_amd64.tar.gz` |
| `mise run build:darwin-arm64` | Build `build/darwin_arm64/` and `build/darwin_arm64.tar.gz` |
| `mise run clean` / `mise run clobber` | Remove generated `build/` artifacts; keep databases, uploads, and local configuration |

Declare source/output tracking for native binary and asset tasks, including
embedded migrations in the binary inputs. Each release task depends on
`build:assets` and invokes `scripts/build-release <os> <arch>`, as in the
[FAM release script](../../../fam/scripts/build-release) and
[Logger4Life release script](../../../logger4life/scripts/build-release).
The script validates the target, builds with the appropriate `GOOS`/`GOARCH`,
copies runtime files, writes the Git revision to `VERSION` (with a dirty-worktree
suffix when applicable), and archives the target directory. A failed build
must fail the task and must not publish a partial archive.

Each archive has this layout at its root:

```text
moneybags
assets/
VERSION
deploy/caddy-handle-template.json
db/tern.conf
db/migrations/postgresql/
```

Jed migrations are embedded in the executable. Include the Caddy template and
PostgreSQL migration files explicitly in packaging; copying only the binary and
assets from the sibling scripts is insufficient for this release contract.
Exclude development data, uploaded files, `.env` files, and local credentials.

## Verna deployment

Use [Verna](https://github.com/jackc/verna) with a prepared Ubuntu host, systemd,
Caddy, and SSH access. Follow its host setup instructions before the initial
application registration. Money Bags must read `PORT`, serve `/health`, and
shut down gracefully on `SIGTERM`, per Verna's
[application contract](https://github.com/jackc/verna#application-contract).

Configure the local deployment target in `mise.local.toml`, following the
[FAM deployment example](../../../fam/README.md#deployment):

```toml
[env]
VERNA_SSH_HOST = "moneybags.example.com"
VERNA_APP = "moneybags"
```

Run commands with the mise environment active. Register the app once:

```sh
verna app init --domain moneybags.example.com \
  --exec-path moneybags --exec-arg server
```

The executable and argument correspond to the Money Bags release layout above.
The pinned Verna 0.8.0 accepts `--caddy-handle-template-path` on **deploy**, not
`app init`; pass it explicitly as shown below. Newer upstream documentation
describes an artifact-template option on initialization, but that option is
absent from the released CLI and current published source inspected here.
Verify commands with the pinned CLI's `--help` when upgrading Verna.

### Runtime configuration

Configure the public origin and passkeys for this application:

```sh
verna app env set BIND_ADDRESS=127.0.0.1
verna app env set SECURE_COOKIES=true
verna app env set WEBAUTHN_RP_ID=moneybags.example.com
verna app env set WEBAUTHN_ORIGIN=https://moneybags.example.com
verna app env set MCP_CANONICAL_URL=https://moneybags.example.com
verna app env set ATTACHMENTS_DIR=/var/lib/verna/apps/moneybags/shared/attachments
```

`MCP_CANONICAL_URL` is the public origin; clients connect to
`https://moneybags.example.com/mcp`. The RP ID omits scheme and port, while the
WebAuthn origin exactly matches the browser-visible origin. Money Bags must not
override the port supplied by Verna. These configuration names follow the
sibling applications.

Choose one database backend. For PostgreSQL, provision the database and
application credentials and set the deployment's connection URL:

```sh
verna app env set DATABASE_BACKEND=postgresql
verna app env set DATABASE_URL
```

The second command uses Verna's hidden value prompt; enter the production
connection URL. See [Verna environment management](https://github.com/jackc/verna#manage-environment-variables).

For Jed instead:

```sh
verna app env set DATABASE_BACKEND=jed
verna app env set JED_DATA_DIR=/var/lib/verna/apps/moneybags/shared/jed
```

Ensure data directories are writable by the application's service account.
Keep attachment and Jed data outside release directories so deployment and
release cleanup preserve them. Switching `DATABASE_BACKEND` does not migrate
data. Back up the selected database and attachment directory together.

### Routing and release

Adapt [FAM's Caddy template](../../../fam/deploy/caddy-handle-template.json).
Serve SvelteKit assets from `assets/`, cache `/_app/immutable/*`, proxy `/api/*`,
`/health`, `/mcp`, `/mcp/*`, `/oauth/*`, and OAuth discovery routes to the Go
server, and fall back to `index.html` for client-side routes. The template must
cover the actual discovery paths published by Money Bags. Attachment downloads
go through authenticated API actions, never static file serving.

Build and deploy for the server's architecture:

```sh
mise run build:linux-amd64 && verna app deploy build/linux_amd64.tar.gz \
  --caddy-handle-template-path deploy/caddy-handle-template.json
```

For an ARM64 server, use `build:linux-arm64` and `build/linux_arm64.tar.gz`.
The Darwin targets produce local macOS artifacts; production Verna deployments
use the Linux targets. This follows the sibling projects' build-and-deploy
workflow. With Verna 0.8.0, deploy from the matching release checkout so its local
Caddy template matches the template packaged in the archive. The archive still
contains the template for inspection and compatibility with artifact-aware
Verna versions.

Inspect the deployed app with `verna app status` and `verna app logs`.
`verna app rollback` selects the preceding release; it does not undo database
changes. See Verna's [deployment and rollback guide](https://github.com/jackc/verna#deploy).

### Data compatibility during deployment

Verna briefly runs the previous and next processes during its
[deployment sequence](https://github.com/jackc/verna#deploy-sequence).
Money Bags must validate concurrent access during that overlap for PostgreSQL
and the pinned Jed version. Authoritative user/access state must be read from
storage; process-local caches must not grant stale access. Coordinate any
cleanup jobs so they cannot delete files still being attached by the other
process.

Apply PostgreSQL migrations through a deliberate release step using the
production connection and packaged migrations. Normal rolling releases require
schema changes compatible with both executable versions. Jed migrations run on
open and must also support overlap, or the release requires a maintenance
window with the old process stopped before the new one opens the files. The
same rule applies to incompatible Jed file-format changes. Retain a restorable
database-plus-attachments backup for such upgrades.

Before shipping the build setup, verify archive contents for each target,
native executable startup, static routes, `/health`, passkey login, MCP OAuth,
attachment persistence, and deploy/rollback against a compatible test database.
