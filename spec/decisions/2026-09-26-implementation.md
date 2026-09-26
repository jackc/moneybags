# Initial implementation decisions

Date: 2026-09-26.

The v1 behavior follows the design defaults: cumulative balances, negative
balances allowed, exactly one bag per entry, no receipt/grouping model, and
passwords plus multiple passkeys. Family members have equal application rights;
OAuth scopes constrain a connection, not the person's role.

The core action catalog defines typed inputs, public access, mutation status,
permission, web/MCP exposure and request limits. In-process typed actions and
dynamic JSON calls use the same authorization and handler pipeline. HTTP and
MCP perform protocol translation. Blob storage and bounded external fetching
are driven ports, and clocks/IDs/tokens are injectable.

Persistence uses dedicated bags, entries and attachments tables, including
`bigint` entry amounts and composite family foreign keys. Extensible auxiliary
records (auth, revisions, audit, idempotency) use a transaction-scoped JSON record
port. Stored JSON is not exposed without the appropriate public result type.
This keeps shared identity/family transaction behavior identical across stores;
core has no SQL or storage-package dependency. Bounded financial pages and exact
grouped sums execute in the adapters.

PostgreSQL serializes each family's action with a transaction advisory lock,
which is deliberately coarser than per-bag locks and makes archive/write races
and aggregate bounds deterministic. All family queries use checked tenant views.
Jed serializes family writes inside the family file; global identity projections
are explicitly separate commits with compensation and administrative repair.
Missing family files are reported rather than recreated by authentication.

The memory adapter is a test fake only. PostgreSQL and the pinned Jed version
have the same shared conformance suite. Development comparison/replication mode
(`DATABASE_BACKEND=both`) remains optional and is not implemented.

External files use HTTPS, bounded bytes, public destination checks at DNS/dial
time, and no forwarded user credentials. Attachment copies have independent
IDs/keys/lifetimes. Multipart uploads and MCP file inputs share core staging.
Ordinary entry/list/revision responses contain metadata only.

Runtime and browser dependencies are pinned. Release archives include the Go
executable, compiled static application, Git version, tern configuration and
PostgreSQL migrations, and Caddy routing template. Native startup, archive
inspection, automated tests and backup restore are local checks; actual ChatGPT
receipt handoff and live Verna deploy/rollback remain deployment acceptance
checks requiring a configured client and test host.

Verna is pinned to released 0.8.0 (published source `34702a638ad7`). Inspection
of the actual CLI found that the design's template flag belongs to `app deploy`,
not `app init`, in this version. The operator guide uses the verified syntax and
requires deploying from the matching checkout. Archives still include the Caddy
template as required. Moving to an artifact-aware Verna release should update
the pin and reverify the command contract together.
