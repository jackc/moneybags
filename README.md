# Money Bags

A small spending tracker for shared family budgets: create a bag, add money,
record expenses, and see how much remains. Use it through the web application
or an MCP client such as ChatGPT. Credits and purchases share one entry model,
with optional Markdown notes and multiple file attachments. Each entry affects
exactly one bag. A receipt covering multiple bags is recorded as separate
entries, which may repeat the notes and attachments. All money is stored as
signed USD cents.

Zero-dollar entries let you add chronological notes to a bag. Entries can be
edited or deleted, and all users in a family have equal access.

The home page lists all active bags, with your personal pins first. Both pinned
and unpinned bags are alphabetical. Use Manage pins to keep everyday bags at the
top; pins are saved to your account across devices. Record expenses directly from a bag's card, and
open the bag to add money or review activity. Use Delete bag on the bag page to
permanently remove it and all its entries and attachments for the family, after
confirmation. Archive a bag instead to preserve its balance and history. Bag amounts are budget guidelines.

The application includes password and passkey authentication, invitations,
shared bags and activity, Markdown notes, independent file attachments, entry
history, account and connected-client management, and an OAuth-protected MCP
endpoint. PostgreSQL and Jed implement the same transactional application ports.

## Add to your phone's home screen

Money Bags is an installable Progressive Web App (PWA). Open the deployed HTTPS
site in Safari on iPhone, choose Share → Add to Home Screen, and enable Open as
Web App if shown. On Android, open it in Chrome and choose Install app or Add to
Home screen from the browser menu. It opens in its own window with the Money Bags
icon. Localhost also supports installation for development.

Budget access and changes require an internet connection. The service worker
caches public app assets and shows a reconnect page when opened offline; it does
not cache account data or attachments or queue changes for later submission.

## Run locally

Install [mise](https://mise.jdx.dev/) and PostgreSQL 18. Then:

```sh
mise trust
mise install
mise run dev:init
mise run dev
```

`dev:init` allocates unique ports for this checkout and installs Go, npm and
Chromium dependencies. `dev` supervises PostgreSQL, migrations, the Go API and
Vite through process-compose. In another terminal, `mise run dev:urls` prints
the web, API and MCP URLs. `mise run dev:wait` checks readiness;
`mise run dev:down` stops the supervisor. Create your family in the web app and
share invitation links from Family settings to add members.

For a standalone Jed server with compiled frontend assets:

```sh
mise run build
DATABASE_BACKEND=jed ALLOW_REGISTRATION=true PORT=4000 MCP_CANONICAL_URL=http://localhost:4000 \
  WEBAUTHN_ORIGIN=http://localhost:4000 WEBAUTHN_RP_ID=localhost \
  JED_DATA_DIR="$PWD/.dev/jed" ATTACHMENTS_DIR="$PWD/.dev/attachments" \
  ./build/moneybags server
```

Open `http://localhost:4000`. The Go executable serves the static frontend;
Node is needed only for development and asset builds. Persist both the selected
database and the attachment directory. Selecting another database backend does
not transfer data.

Registration is disabled by default, matching FAM and Logger4Life. Enable new
accounts and families with `ALLOW_REGISTRATION=true` or
`./build/moneybags server --allow-registration`. The CLI flag overrides the
environment; `--allow-registration=false` explicitly disables it. Restart the
server after changing the setting. Existing users can still sign in, and family
invitation links still work when registration is disabled. Local development
and browser tests enable registration automatically.

Logging defaults to JSON on stderr. Set `LOG_FORMAT=text` for readable console
logs or `LOG_FORMAT=journal` to log directly to systemd journald, as in Logger4Life
and FAM. `LOG_LEVEL` accepts `debug`, `info` (default), `warn`, or `error`.

## Verify and build

```sh
mise run test:backend     # Go race detector, core, both stores, HTTP and MCP
mise run test:frontend    # Svelte checks, exact money and safe Markdown tests
mise run test:browser     # isolated Jed server; Chromium, mobile and passkeys
mise run test:operations  # native startup, coherent backups and restore checks
mise run test            # all of the above
mise run build
mise run build:linux-amd64
```

PostgreSQL tests use `TEST_DATABASE_URL` and skip when it is absent; the mise
configuration points it at the supervised local test database. Browser tests
start their own backend and Vite on the checkout's test ports. On an OS newer
than Playwright's supported list, use its compatible platform override when
installing Chromium (for example `PLAYWRIGHT_HOST_PLATFORM_OVERRIDE=ubuntu24.04-arm64`).
An existing compatible Chromium installation can be selected with
`PLAYWRIGHT_CHROMIUM_EXECUTABLE=/absolute/path/to/chrome`; this override was used
for browser verification on the current Ubuntu 26.04 host.

Release targets are Linux/macOS on amd64/arm64. Each archive includes the
executable, assets, version, PostgreSQL migrations and Caddy template.
`mise run clean` removes only generated build output.

## Interfaces and operations

The web adapter calls `POST /api/actions/{name}` with JSON, a session cookie and
an exact matching `Origin`. Multipart uploads use `POST /api/uploads`; attachment
downloads require authentication at `/api/attachments/{id}`. Product actions
are enumerated from the same private-by-default core catalog as MCP tools.

MCP clients connect to the configured public origin's `/mcp`. OAuth discovery,
CIMD client metadata, S256 PKCE, explicit browser consent, scoped access tokens,
refresh rotation and revocation are implemented. Available scopes are
`bags:read`, `bags:write` and `family:write`. Every call checks the current account.
Browser cookies do not authenticate MCP requests. Reuse a mutation's
`request_id` and inputs when retrying; use a new ID for a separate entry.

All amounts and balances use integer USD cents within JavaScript's exact integer
range. Per-file limit: 5 MiB; combined file inputs: 20 MiB; notes: 64 KiB;
default family storage quota: 1 GiB, configurable with `FAMILY_STORAGE_QUOTA_BYTES`.
External file inputs require a stable `file_id` and a public HTTPS download URL.
The server stores the bytes before reporting an attachment saved.

See [the design](spec/design/money-bags.md),
[build and Verna deployment](spec/design/build-and-deployment.md),
[operations and backup/restore](docs/operations.md), and
[implementation decisions and release checks](spec/decisions/2026-09-26-implementation.md).
