<script>
  import { openDialog } from '$lib/dialog.js';
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import { action, mutation, items, requestID, friendlyError } from './api.js';
  import { money, dateLabel } from './money.js';
  import { renderNotes, noteSummary } from './markdown.js';
  import Auth from './components/Auth.svelte';
  import BagIcon from './components/BagIcon.svelte';
  import BagForm from './components/BagForm.svelte';
  import BagHome from './components/BagHome.svelte';
  import EntryForm from './components/EntryForm.svelte';
  import Settings from './components/Settings.svelte';
  import OAuthConsent from './components/OAuthConsent.svelte';
  let ready = false,
    user = null,
    family = null,
    error = '',
    toast = '',
    loading = false;
  let bags = [],
    bag = null,
    entries = [],
    entry = null,
    revisions = [],
    cursor = '',
    historyCursor = '',
    pinnedBagIDs = [],
    entryBagID = '';
  let modal = '',
    entryMode = 'expense',
    editEntry = null,
    editBag = null;
  let loadedPath = '',
    routeGeneration = 0,
    busy = false;
  $: path = $page.url.pathname;
  $: invitation =
    $page.url.searchParams.get('token') ||
    (path.startsWith('/invite/') ? decodeURIComponent(path.slice(8)) : '');
  $: bagID = path.startsWith('/bags/') ? decodeURIComponent(path.slice(6)) : '';
  $: entryID = path.startsWith('/entries/') ? decodeURIComponent(path.slice(9)) : '';
  $: if (ready && user && path !== loadedPath) {
    loadedPath = path;
    loadRoute();
  }

  onMount(async () => {
    try {
      const result = await action('whoami');
      user = result.user;
      family = result.family;
    } catch (e) {
      if (e.status !== 401) error = friendlyError(e);
    } finally {
      ready = true;
    }
  });
  async function loggedIn(result) {
    family = result.family;
    if (path.startsWith('/invite') || path === '/login' || path === '/register') await goto('/');
    loadedPath = '';
    user = result.user;
  }
  async function loadBags() {
    let all = [],
      next = '';
    do {
      const result = await action('list_bags', { limit: 100, ...(next ? { cursor: next } : {}) });
      all = [...all, ...items(result, 'bags')];
      next = result.next_cursor || '';
    } while (next);
    return all;
  }
  async function loadRoute() {
    const generation = ++routeGeneration;
    loading = true;
    error = '';
    bag = null;
    entry = null;
    modal = '';
    try {
      const [allBags, preferences] = await Promise.all([loadBags(), action('get_bag_preferences')]);
      if (generation !== routeGeneration) return;
      bags = allBags;
      pinnedBagIDs = preferences.pinned_bag_ids;
      if (bagID) {
        const [bagResult, activity] = await Promise.all([
          action('get_bag', { bag_id: bagID }),
          action('list_entries', { bag_id: bagID, limit: 30 })
        ]);
        if (generation !== routeGeneration) return;
        bag = bagResult.bag || bagResult;
        entries = items(activity, 'entries');
        cursor = activity.next_cursor || '';
      } else if (entryID) {
        const [result, history] = await Promise.all([
          action('get_entry', { entry_id: entryID }),
          action('get_entry_history', { entry_id: entryID, limit: 30 })
        ]);
        if (generation !== routeGeneration) return;
        entry = result.entry || result;
        revisions = items(history, 'revisions');
        historyCursor = history.next_cursor || '';
        bag = bags.find((b) => b.id === entry.bag_id) || null;
      }
    } catch (e) {
      if (generation === routeGeneration) error = friendlyError(e);
    } finally {
      if (generation === routeGeneration) loading = false;
    }
  }
  async function moreEntries() {
    busy = true;
    try {
      const result = await action('list_entries', { bag_id: bagID, cursor, limit: 30 });
      entries = [...entries, ...items(result, 'entries')];
      cursor = result.next_cursor || '';
    } catch (e) {
      error = friendlyError(e);
    } finally {
      busy = false;
    }
  }
  async function moreHistory() {
    busy = true;
    try {
      const result = await action('get_entry_history', {
        entry_id: entryID,
        cursor: historyCursor,
        limit: 30
      });
      revisions = [...revisions, ...items(result, 'revisions')];
      historyCursor = result.next_cursor || '';
    } catch (e) {
      error = friendlyError(e);
    } finally {
      busy = false;
    }
  }
  function openEntry(mode, selectedBag = bagID) {
    entryBagID = selectedBag;
    entryMode = mode;
    editEntry = null;
    modal = 'entry';
  }
  async function saved(label) {
    modal = '';
    toast = label;
    await loadRoute();
  }
  async function toggleArchive() {
    busy = true;
    try {
      await mutation(bag.archived ? 'unarchive_bag' : 'archive_bag', {
        bag_id: bag.id,
        expected_version: bag.version
      });
      toast = bag.archived ? 'Bag is active again.' : 'Bag archived. Its history is still here.';
      await loadRoute();
    } catch (e) {
      error = friendlyError(e);
    } finally {
      busy = false;
    }
  }
  let deleteRequest = '';
  async function deleteBag() {
    busy = true;
    error = '';
    try {
      await action('delete_bag', {
        bag_id: bag.id,
        expected_version: bag.version,
        request_id: deleteRequest
      });
      modal = '';
      toast = 'Bag deleted, including its entries and attachments.';
      await goto('/');
    } catch (e) {
      error = friendlyError(e);
    } finally {
      busy = false;
    }
  }
  async function deleteEntry() {
    busy = true;
    try {
      await action('delete_entry', {
        entry_id: entry.id,
        expected_version: entry.version,
        request_id: deleteRequest
      });
      modal = '';
      toast = 'Entry deleted. The bag balance has been updated.';
      await goto(`/bags/${entry.bag_id}`);
    } catch (e) {
      error = friendlyError(e);
    } finally {
      busy = false;
    }
  }
  async function logout() {
    try {
      await action('logout');
      user = null;
      family = null;
      await goto('/');
    } catch (e) {
      error = friendlyError(e);
    }
  }
</script>

<svelte:head
  ><title
    >{bagID && bag
      ? bag.name + ' · '
      : entryID
        ? 'Entry · '
        : path === '/settings'
          ? 'Settings · '
          : ''}Money Bags</title
  ><meta
    name="description"
    content="Simple, shared spending bags for your family. Know what's left."
  /></svelte:head
>

{#if !ready}<main class="initial-loading" aria-live="polite">
    <BagIcon size={48} />
    <p>Opening your bags…</p>
  </main>
{:else if !user}<Auth onlogin={loggedIn} {invitation} />{#if error}<div
      class="connection-error notice error"
      role="alert"
    >
      {error}<button class="text-button" on:click={() => location.reload()}>Retry</button>
    </div>{/if}
{:else}
  <a href="#main" class="skip-link">Skip to content</a>
  <header class="app-header">
    <div class="header-inner">
      <a class="brand" href="/"
        ><BagIcon size={30} /><span>money bags<span class="brand-dot">.</span></span></a
      >
      <nav aria-label="Main navigation">
        <a
          href="/"
          class:active={!path.startsWith('/settings') && !path.startsWith('/authorize')}
          aria-current={path === '/' ? 'page' : undefined}>Your bags</a
        ><a
          href="/settings"
          class:active={path.startsWith('/settings')}
          aria-current={path === '/settings' ? 'page' : undefined}>Settings</a
        >
      </nav>
      <button
        class="avatar"
        title={`Sign out ${user.display_name || user.username}`}
        aria-label="Sign out"
        on:click={logout}
        >{(user.display_name || user.username || '?').slice(0, 1).toUpperCase()}</button
      >
    </div>
  </header>
  <main id="main" class="app-main">
    {#if toast}<div class="notice success" role="status">
        <span>{toast}</span><button
          class="icon-button"
          aria-label="Dismiss notification"
          on:click={() => (toast = '')}>×</button
        >
      </div>{/if}
    {#if error}<div class="notice error" role="alert">
        <span>{error}</span><button class="text-button" on:click={loadRoute}>Refresh</button>
      </div>{/if}
    {#if path.startsWith('/settings')}
      <Settings
        {user}
        {family}
        onfamily={(updated) => (family = updated)}
        ondeleted={async () => {
          user = null;
          family = null;
          await goto('/');
        }}
      />
    {:else if path.startsWith('/authorize')}
      <OAuthConsent {family} />
    {:else if loading}<div class="loading" aria-live="polite">Gathering the latest…</div>
    {:else if entryID && entry}
      <a class="back-link" href={`/bags/${entry.bag_id}`}>← {bag?.name || 'Back to bag'}</a>
      <div class="page-heading">
        <div>
          <p class="eyebrow">{dateLabel(entry.date)}</p>
          <h1>
            {entry.amount_cents < 0
              ? 'An expense'
              : entry.amount_cents > 0
                ? 'Money added'
                : 'A note for the record'}
          </h1>
          <p class="muted">
            In {bag?.name || 'this bag'} · Recorded by {entry.author_name ||
              entry.author?.display_name ||
              'a family member'}
          </p>
        </div>
        <div class="actions">
          <button
            class="secondary"
            disabled={bag?.archived}
            on:click={() => {
              editEntry = entry;
              modal = 'entry';
            }}>Edit entry</button
          ><button
            class="secondary danger"
            on:click={() => {
              deleteRequest = requestID();
              modal = 'delete';
            }}>Delete entry</button
          >
        </div>
      </div>
      <section class="entry-card">
        <div class="entry-amount" class:negative={entry.amount_cents < 0}>
          {money(entry.amount_cents, true)}
        </div>
        <p class="muted">
          {entry.amount_cents === 0
            ? 'No change to the balance'
            : entry.amount_cents < 0
              ? 'Subtracted from this bag'
              : 'Added to this bag'}
        </p>
        <hr />
        {#if entry.notes}<div class="markdown">{@html renderNotes(entry.notes)}</div>{:else}<p
            class="muted"
          >
            No notes for this entry.
          </p>{/if}
        {#if entry.attachments?.length}<h3 class="attachment-heading">
            Attachments <span class="count">{entry.attachments.length}</span>
          </h3>
          <ul class="attachment-list">
            {#each entry.attachments as file}<li>
                <span aria-hidden="true">↧</span><a
                  href={`/api/attachments/${encodeURIComponent(file.id)}`}
                  download>{file.file_name || file.filename}</a
                ><small>{Math.ceil((file.byte_length || file.size || 0) / 1024)} KB</small>
              </li>{/each}
          </ul>{/if}
      </section>
      <section class="history">
        <h2>Change history</h2>
        <p class="field-help">
          History preserves file details. Removed files are no longer available to download.
        </p>
        {#each revisions as revision}<details class="history-item">
            <summary
              ><span
                >Version {revision.version} · {revision.actor_name ||
                  revision.actor?.display_name ||
                  (revision.actor_id === user.id
                    ? user.display_name || user.username
                    : 'Family member')}</span
              ><small
                >{revision.created_at ? new Date(revision.created_at).toLocaleString() : ''}</small
              ></summary
            >
            <div class="history-detail">
              {#if (revision.current ? { ...revision.current.entry, attachments: revision.current.attachments } : null) || revision.after || revision.new_snapshot || revision.snapshot}{@const snapshot =
                  (revision.current
                    ? { ...revision.current.entry, attachments: revision.current.attachments }
                    : null) ||
                  revision.after ||
                  revision.new_snapshot ||
                  revision.snapshot}<strong>{money(snapshot.amount_cents ?? 0, true)}</strong>
                <div class="markdown">{@html renderNotes(snapshot.notes || '')}</div>
                {#if snapshot.attachments?.length}<p>Files recorded in this version:</p>
                  <ul>
                    {#each snapshot.attachments as file}<li>
                        {file.file_name || file.filename}
                      </li>{/each}
                  </ul>{/if}{:else}<p>
                  Entry updated through {revision.source || 'Money Bags'}.
                </p>{/if}
            </div>
          </details>{/each}{#if !revisions.length}<p class="muted">
            No changes recorded.
          </p>{/if}{#if historyCursor}<button
            class="secondary"
            on:click={moreHistory}
            disabled={busy}>Load more history</button
          >{/if}
      </section>
    {:else if bagID && bag}
      <a class="back-link" href="/">← All bags</a>
      <section class="bag-hero">
        <div>
          <p class="eyebrow">{bag.archived ? 'ARCHIVED BAG' : 'A PLACE FOR YOUR PLANS'}</p>
          <h1>{bag.name}</h1>
          {#if bag.description}<p class="bag-description">{bag.description}</p>{/if}
          <div class="hero-balance" class:negative={bag.balance_cents < 0}>
            {money(bag.balance_cents)}
          </div>
          <p class="balance-label">
            {bag.balance_cents < 0
              ? 'Negative balance · spending exceeds money added'
              : 'remaining in this bag'}
          </p>
        </div>
        <div class="hero-illustration" aria-hidden="true">
          <BagIcon size={118} /><span>little by little</span>
        </div>
      </section>
      <div class="bag-toolbar">
        <div class="actions">
          <button class="primary" disabled={bag.archived} on:click={() => openEntry('expense')}
            >− Record expense</button
          ><button class="secondary" disabled={bag.archived} on:click={() => openEntry('credit')}
            >+ Add money</button
          >
        </div>
        <div class="actions">
          <button
            class="text-button"
            on:click={() => {
              editBag = bag;
              modal = 'bag';
            }}>Edit bag</button
          ><button class="text-button" disabled={busy} on:click={toggleArchive}
            >{bag.archived ? 'Unarchive' : 'Archive'}</button
          ><button
            class="text-button danger"
            disabled={busy}
            on:click={() => {
              error = '';
              deleteRequest = requestID();
              modal = 'delete-bag';
            }}>Delete bag</button
          >
        </div>
      </div>
      {#if bag.archived}<div class="notice">
          This bag is archived. Unarchive it to add or edit entries. Existing entries can still be
          deleted.
        </div>{/if}
      <div class="section-heading activity-heading">
        <h2>Activity</h2>
        <span class="muted">Newest first</span>
      </div>
      {#if !entries.length}<div class="empty-state">
          <BagIcon size={45} />
          <h3>A clean start</h3>
          <p>Add some money, record an expense, or leave a zero-dollar note.</p>
        </div>{:else}<div class="activity-list">
          {#each entries as item}<a class="activity-row" href={`/entries/${item.id}`}
              ><span class="activity-icon" class:credit={item.amount_cents > 0} aria-hidden="true"
                >{item.amount_cents < 0 ? '↗' : item.amount_cents > 0 ? '↙' : '≡'}</span
              >
              <div class="activity-copy">
                <strong
                  >{noteSummary(item.notes) ||
                    (item.amount_cents < 0
                      ? 'Expense'
                      : item.amount_cents > 0
                        ? 'Money added'
                        : 'Zero-dollar note')}</strong
                ><span
                  >{dateLabel(item.date)}{item.author_name ? ` · ${item.author_name}` : ''}{item
                    .attachments?.length
                    ? ` · ${item.attachments.length} file(s)`
                    : ''}</span
                >
              </div>
              <span class="activity-amount" class:credit={item.amount_cents > 0}
                >{money(item.amount_cents, true)}</span
              ><span class="row-arrow" aria-hidden="true">›</span></a
            >{/each}
        </div>{/if}
      {#if cursor}<div class="load-more">
          <button class="secondary" disabled={busy} on:click={moreEntries}
            >{busy ? 'Loading…' : 'Load more activity'}</button
          >
        </div>{/if}
    {:else}
      <BagHome
        {bags}
        {family}
        {pinnedBagIDs}
        onpins={(ids) => (pinnedBagIDs = ids)}
        onexpense={(id) => openEntry('expense', id)}
        oncreate={() => {
          editBag = null;
          modal = 'bag';
        }}
      />
    {/if}
  </main>
  <footer class="app-footer">
    <span>Room for the everyday.</span><span>Money Bags · USD</span>
  </footer>
  {#if modal === 'bag'}<BagForm
      bag={editBag}
      onsaved={() => saved(editBag ? 'Bag updated.' : 'Your new bag is ready.')}
      oncancel={() => (modal = '')}
    />{/if}
  {#if modal === 'entry'}<EntryForm
      {bags}
      {family}
      entry={editEntry}
      selectedBag={entryBagID}
      mode={entryMode}
      onsaved={() => saved(editEntry ? 'Entry updated.' : 'Entry saved. Everything is up to date.')}
      oncancel={() => (modal = '')}
    />{/if}
  {#if modal === 'delete-bag'}<div class="sheet-backdrop" role="presentation">
      <dialog
        class="sheet small"
        aria-labelledby="delete-bag-title"
        aria-describedby="delete-bag-description"
        use:openDialog={() => {
          if (!busy) modal = '';
        }}
      >
        <h2 id="delete-bag-title">Delete {bag.name}?</h2>
        <p id="delete-bag-description">
          This permanently deletes this bag and all its entries, notes, files, and change history
          for everyone in your family. This cannot be undone.
        </p>
        <p>To keep its balance and history, archive the bag instead.</p>
        {#if error}<div class="notice error" role="alert">{error}</div>{/if}
        <div class="form-actions">
          <button class="secondary" disabled={busy} on:click={() => (modal = '')}>Keep bag</button
          ><button class="danger-button" disabled={busy} on:click={deleteBag}
            >{busy ? 'Deleting…' : 'Delete bag'}</button
          >
        </div>
      </dialog>
    </div>{/if}
  {#if modal === 'delete'}<div class="sheet-backdrop" role="presentation">
      <dialog
        class="sheet small"
        aria-labelledby="delete-title"
        use:openDialog={() => {
          if (!busy) modal = '';
        }}
      >
        <h2 id="delete-title">Delete this entry?</h2>
        <p>
          This removes {money(entry.amount_cents, true)} from the history and updates the bag balance.
          Its notes, files, and change history will be permanently deleted.
        </p>
        {#if error}<div class="notice error" role="alert">{error}</div>{/if}
        <div class="form-actions">
          <button class="secondary" disabled={busy} on:click={() => (modal = '')}>Keep entry</button
          ><button class="danger-button" disabled={busy} on:click={deleteEntry}
            >{busy ? 'Deleting…' : 'Delete entry'}</button
          >
        </div>
      </dialog>
    </div>{/if}
{/if}
