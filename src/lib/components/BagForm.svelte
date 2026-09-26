<script>
  import { openDialog } from '$lib/dialog.js';
  import { action, requestID, friendlyError } from '$lib/api.js';
  import { parseDollars } from '$lib/money.js';
  export let bag = null;
  export let onsaved;
  export let oncancel;
  let name = bag?.name || '',
    description = bag?.description || '',
    initial = '',
    error = '',
    busy = false;
  let request_id = requestID(),
    previous = '';
  async function save() {
    busy = true;
    error = '';
    try {
      const payload = {
        name,
        description,
        ...(bag
          ? { bag_id: bag.id, expected_version: bag.version }
          : initial.trim()
            ? { initial_amount_cents: parseDollars(initial) }
            : {})
      };
      const signature = JSON.stringify(payload);
      if (previous && previous !== signature) request_id = requestID();
      previous = signature;
      const result = await action(bag ? 'update_bag' : 'create_bag', { ...payload, request_id });
      await onsaved(result);
    } catch (e) {
      error = friendlyError(e);
    } finally {
      busy = false;
    }
  }
</script>

<div class="sheet-backdrop" role="presentation">
  <dialog
    class="sheet small"
    aria-labelledby="bag-title"
    use:openDialog={() => {
      if (!busy) oncancel();
    }}
  >
    <div class="section-heading">
      <div>
        <p class="eyebrow">MAKE A LITTLE ROOM</p>
        <h2 id="bag-title">{bag ? 'Edit bag' : 'A new bag'}</h2>
      </div>
      <button class="icon-button" aria-label="Close bag form" on:click={oncancel} disabled={busy}
        >×</button
      >
    </div>
    {#if error}<div class="notice error" role="alert">{error}</div>{/if}
    <form on:submit|preventDefault={save}>
      <label
        >Bag name<input
          bind:value={name}
          required
          maxlength="120"
          placeholder="Groceries, weekends, a rainy day…"
        /></label
      >
      <label
        >Description <span class="optional">optional</span><textarea
          bind:value={description}
          rows="3"
          maxlength="2000"
          placeholder="What is this money for?"></textarea></label
      >
      {#if !bag}<label
          >Starting amount (USD) <span class="optional">optional</span>
          <div class="amount-input">
            <span>$</span><input
              aria-label="Starting amount (USD)"
              bind:value={initial}
              inputmode="decimal"
              placeholder="0.00"
            />
          </div>
          <span class="field-help">Adds a normal money-in entry. Leave blank to start at zero.</span
          ></label
        >{/if}
      <div class="form-actions">
        <button class="secondary" type="button" on:click={oncancel} disabled={busy}>Cancel</button
        ><button class="primary" type="submit" disabled={busy}
          >{busy ? 'Saving…' : bag ? 'Save bag' : 'Create bag'}</button
        >
      </div>
    </form>
  </dialog>
</div>
