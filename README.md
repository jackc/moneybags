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

The project is currently in the design stage. See the
[design document](spec/design/money-bags.md) for the proposed product,
architecture, data model, and implementation sequence.

The [build and deployment guide](spec/design/build-and-deployment.md) documents
the planned mise build tasks, release archives, and Verna deployment workflow,
following FAM and Logger4Life. These tasks will be added when the application
is scaffolded.
