<script>
  import { tick } from 'svelte';
  import BagIcon from './BagIcon.svelte';
  import { money } from '$lib/money.js';
  import { action, mutation, friendlyError } from '$lib/api.js';

  export let bags = [];
  export let family;
  export let pinnedBagIDs = [];
  export let onpins;
  export let onexpense;
  export let oncreate;

  let archived = false,
    arranging = false,
    savingPin = '',
    error = '',
    status = '';
  $: pinnedIDs = new Set(pinnedBagIDs);
  $: visibleBags = bags
    .filter((bag) => !!bag.archived === archived)
    .sort((a, b) => {
      if (!archived) {
        const aPinned = pinnedIDs.has(a.id);
        const bPinned = pinnedIDs.has(b.id);
        if (aPinned !== bPinned) return aPinned ? -1 : 1;
      }
      return (
        a.name.localeCompare(b.name, undefined, { sensitivity: 'base', numeric: true }) ||
        a.id.localeCompare(b.id)
      );
    });
  $: hasActivePins = bags.some((bag) => !bag.archived && pinnedIDs.has(bag.id));
  $: archivedCount = bags.filter((bag) => bag.archived).length;

  async function togglePin(bag, button) {
    const pinned = !pinnedIDs.has(bag.id);
    savingPin = bag.id;
    error = '';
    status = '';
    try {
      await mutation('set_bag_pin', { bag_id: bag.id, pinned });
      // Refresh after a replay as well: another device may have changed pins
      // since the original request committed.
      const preferences = await action('get_bag_preferences');
      onpins(preferences.pinned_bag_ids);
      status = pinned
        ? `${bag.name} is kept at the top of your list.`
        : `${bag.name} moved to your unpinned bags.`;
    } catch (e) {
      error = friendlyError(e);
    } finally {
      savingPin = '';
      await tick();
      if (button.isConnected) button.focus({ preventScroll: true });
    }
  }

  function showArchived(value) {
    archived = value;
    arranging = false;
    status = '';
    error = '';
  }
</script>

<section class="bag-home" aria-labelledby="bags-title">
  <div class="home-heading">
    <div>
      <p class="eyebrow">{family?.name || 'YOUR FAMILY'} · SHARED SPENDING</p>
      <h1 id="bags-title">{archived ? 'Archived bags' : 'Your bags'}</h1>
    </div>
    {#if !archived && visibleBags.length}
      <button
        class="text-button arrange-button"
        disabled={!!savingPin}
        aria-pressed={arranging}
        on:click={() => {
          arranging = !arranging;
          status = '';
        }}>{arranging ? 'Done' : 'Manage pins'}</button
      >
    {/if}
  </div>
  {#if error}<div class="notice error" role="alert">{error}</div>{/if}
  <p class="sr-only" role="status" aria-live="polite">{status}</p>
  {#if !archived && visibleBags.length && (arranging || !hasActivePins)}
    <p class="arrange-hint">
      {arranging
        ? 'Pinned bags appear first. Each group is alphabetical. Only your view changes.'
        : 'Use Manage pins to keep your everyday bags at the top.'}
    </p>
  {/if}

  {#if !visibleBags.length}
    <div class="empty-state">
      <BagIcon size={48} />
      <h2>
        {archived ? 'Nothing tucked away' : bags.length ? 'No active bags' : 'Start with a bag'}
      </h2>
      <p>
        {archived
          ? 'Archived bags keep their balances and history here.'
          : bags.length
            ? 'Create a bag, or open an archived bag to use it again.'
            : 'Create your family’s first bag, like Groceries. Everyone in your family can use it.'}
      </p>
      {#if !archived}<button class="primary" on:click={oncreate}
          >{bags.length ? 'Create a bag' : 'Create your first bag'}</button
        >{/if}
    </div>
  {:else}
    <div class="home-bag-grid">
      {#each visibleBags as item (item.id)}
        <article class="home-bag" aria-label={item.name}>
          <a class="home-bag-details" href={`/bags/${item.id}`}>
            <span class="home-bag-symbol"><BagIcon size={26} /></span>
            <div class="home-bag-copy">
              <h2>{item.name}</h2>
              {#if !archived && pinnedIDs.has(item.id)}<span class="pin-caption">Pinned</span>{/if}
            </div>
            <div class="home-bag-balance">
              <span class="home-bag-amount" class:negative={item.balance_cents < 0}
                >{money(item.balance_cents)}</span
              >
              <span class="home-bag-caption"
                >{item.balance_cents < 0 ? 'over budget' : 'budget left'}</span
              >
            </div>
          </a>
          {#if !archived}
            {#if arranging}
              <button
                class="secondary pin-button"
                class:pinned={pinnedIDs.has(item.id)}
                aria-label={`${pinnedIDs.has(item.id) ? 'Unpin' : 'Pin'} ${item.name}`}
                aria-pressed={pinnedIDs.has(item.id)}
                disabled={!!savingPin}
                on:click={(event) => togglePin(item, event.currentTarget)}
                ><svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true"
                  ><path
                    d="M9 3h6m-5 0v6l-4 4v2h12v-2l-4-4V3M12 15v6"
                    stroke="currentColor"
                    stroke-width="1.6"
                    stroke-linecap="round"
                    stroke-linejoin="round"
                  /></svg
                >{savingPin === item.id
                  ? 'Saving…'
                  : pinnedIDs.has(item.id)
                    ? 'Kept at top'
                    : 'Keep at top'}</button
              >
            {:else}
              <button
                class="primary home-expense"
                aria-label={`Record expense in ${item.name}`}
                on:click={() => onexpense(item.id)}>Record expense</button
              >
            {/if}
          {/if}
        </article>
      {/each}
    </div>
  {/if}

  <div class="home-tools">
    {#if visibleBags.length}<span>Amounts are budget guidelines.</span>{/if}
    <div class="home-tool-actions">
      {#if archived}
        <button class="text-button" on:click={() => showArchived(false)}>← Active bags</button>
      {:else if archivedCount}
        <button class="text-button" disabled={!!savingPin} on:click={() => showArchived(true)}
          >Archived bags ({archivedCount})</button
        >
      {/if}
      {#if bags.length}<button class="text-button" disabled={!!savingPin} on:click={oncreate}
          >+ New bag</button
        >{/if}
    </div>
  </div>
</section>

<style>
  .home-heading {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 18px;
    margin-bottom: 22px;
  }
  .home-heading h1 {
    font-size: 32px;
  }
  .arrange-button {
    min-height: 44px;
    padding: 10px 4px;
  }
  .arrange-hint {
    padding: 12px 16px;
    background: #eaf0df;
    color: #435d43;
    border-radius: 10px;
    font-size: 13px;
    margin-bottom: 16px;
  }
  .home-bag-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 16px;
  }
  .home-bag {
    min-width: 0;
    padding: 17px;
    background: #fff;
    border: 1px solid var(--line);
    border-radius: 12px;
  }
  .home-bag-details {
    display: flex;
    align-items: center;
    gap: 13px;
    min-height: 48px;
    border-radius: 5px;
  }
  .home-bag-details:hover h2 {
    text-decoration: underline;
  }
  .home-bag-symbol {
    width: 38px;
    height: 38px;
    display: flex;
    align-items: center;
    justify-content: center;
    background: #eaf1df;
    color: #698245;
    border-radius: 10px;
    flex-shrink: 0;
  }
  .home-bag-copy {
    flex: 1;
    min-width: 0;
  }
  .home-bag-copy h2 {
    font-size: 17px;
    overflow-wrap: anywhere;
  }
  .pin-caption {
    display: block;
    color: #657663;
    font-size: 11px;
    margin-top: 3px;
  }
  .home-bag-balance {
    text-align: right;
    min-width: 0;
    max-width: 55%;
  }
  .home-bag-amount {
    display: block;
    font-family: 'Manrope Variable', sans-serif;
    font-size: 24px;
    font-weight: 600;
    letter-spacing: -0.045em;
    font-variant-numeric: tabular-nums;
    overflow-wrap: anywhere;
    line-height: 1.3;
  }
  .home-bag-caption {
    display: block;
    font-size: 11px;
    color: #657663;
    margin-top: 4px;
  }
  .home-expense,
  .pin-button {
    width: 100%;
    margin-top: 13px;
  }
  .pin-button.pinned {
    background: #eaf0df;
    border-color: var(--green);
  }
  .home-tools {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    flex-wrap: wrap;
    margin-top: 22px;
    color: #657663;
    font-size: 11px;
  }
  .home-tool-actions {
    display: flex;
    align-items: center;
    gap: 20px;
    flex-wrap: wrap;
    margin-left: auto;
  }
  .home-tool-actions button {
    min-height: 44px;
  }
  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
  }
  @media (max-width: 600px) {
    .home-heading {
      margin-bottom: 16px;
    }
    .home-heading h1 {
      font-size: 27px;
    }
    .home-heading .eyebrow {
      font-size: 9px;
      margin-bottom: 5px;
    }
    .home-bag-grid {
      grid-template-columns: 1fr;
      gap: 11px;
    }
    .home-bag {
      padding: 12px 13px;
    }
    .home-bag-details {
      gap: 9px;
      min-height: 44px;
    }
    .home-bag-copy h2 {
      font-size: 15px;
    }
    .home-bag-symbol {
      width: 32px;
      height: 32px;
    }
    .home-bag-amount {
      font-size: 18px;
    }
    .home-expense,
    .pin-button {
      margin-top: 11px;
    }
  }
  @media (max-width: 360px) {
    .home-bag-symbol {
      display: none;
    }
  }
</style>
