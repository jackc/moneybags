<script>
  import { openDialog } from '$lib/dialog.js';
  import { action, requestID, stageFile, friendlyError } from '$lib/api.js';
  import { parseDollars, dollarsInput, familyToday } from '$lib/money.js';
  export let bags = [];
  export let family;
  export let entry = null;
  export let selectedBag = '';
  export let mode = 'expense';
  export let onsaved;
  export let oncancel;
  let bag_id = entry?.bag_id || selectedBag || bags.find((b) => !b.archived)?.id || '';
  let direction = entry ? (entry.amount_cents < 0 ? 'expense' : 'credit') : mode;
  let amount = entry ? dollarsInput(entry.amount_cents) : '';
  let date = entry?.date?.slice(0, 10) || familyToday(family?.time_zone);
  let notes = entry?.notes || '';
  let files = [],
    removed = [],
    busy = false,
    error = '',
    progress = '';
  let request_id = requestID();
  // Keep staged IDs and the same request key after a lost response. Editing a
  // payload starts a new logical request; an unchanged retry cannot duplicate it.
  let priorPayload = '',
    uploaded = new Map();
  function selectFiles(event) {
    files = [...files, ...Array.from(event.currentTarget.files || [])];
    event.currentTarget.value = '';
  }
  async function save() {
    busy = true;
    error = '';
    try {
      const amount_cents = parseDollars(amount, direction === 'expense' ? -1 : 1);
      if (date > familyToday(family?.time_zone))
        throw new Error('Choose today or an earlier date.');
      if (files.some((file) => file.size > 5 * 1024 * 1024))
        throw new Error('Each file must be 5 MiB or smaller.');
      const attachment_upload_ids = [];
      for (let i = 0; i < files.length; i++) {
        const file = files[i];
        progress = `Uploading ${i + 1} of ${files.length}…`;
        if (!uploaded.has(file)) uploaded.set(file, { request_id: requestID() });
        const staged = uploaded.get(file);
        if (!staged.upload_id) Object.assign(staged, await stageFile(file, staged.request_id));
        attachment_upload_ids.push(staged.upload_id);
      }
      const payload = {
        amount_cents,
        date,
        notes,
        attachment_upload_ids,
        ...(entry
          ? { entry_id: entry.id, expected_version: entry.version, remove_attachment_ids: removed }
          : { bag_id })
      };
      const signature = JSON.stringify(payload);
      if (priorPayload && priorPayload !== signature) request_id = requestID();
      priorPayload = signature;
      progress = 'Saving entry…';
      const result = await action(entry ? 'update_entry' : 'create_entry', {
        ...payload,
        request_id
      });
      await onsaved(result);
    } catch (e) {
      error = friendlyError(e);
    } finally {
      busy = false;
      progress = '';
    }
  }
</script>

<div class="sheet-backdrop" role="presentation">
  <dialog
    class="sheet"
    aria-labelledby="entry-title"
    use:openDialog={() => {
      if (!busy) oncancel();
    }}
  >
    <div class="section-heading">
      <div>
        <p class="eyebrow">ONE SMALL UPDATE</p>
        <h2 id="entry-title">
          {entry ? 'Edit entry' : direction === 'expense' ? 'Record expense' : 'Add money'}
        </h2>
      </div>
      <button class="icon-button" aria-label="Close entry form" on:click={oncancel} disabled={busy}
        >×</button
      >
    </div>
    {#if error}<div class="notice error" role="alert">{error}</div>{/if}
    <form on:submit|preventDefault={save}>
      <div class="segmented">
        <button
          type="button"
          class:chosen={direction === 'expense'}
          aria-pressed={direction === 'expense'}
          on:click={() => (direction = 'expense')}>Expense</button
        ><button
          type="button"
          class:chosen={direction !== 'expense'}
          aria-pressed={direction !== 'expense'}
          on:click={() => (direction = 'credit')}>Money in</button
        >
      </div>
      <label
        >Bag<select bind:value={bag_id} required disabled={!!entry}
          >{#each bags.filter((b) => !b.archived || b.id === bag_id) as bag}<option value={bag.id}
              >{bag.name}</option
            >{/each}</select
        ></label
      >
      <div class="form-grid">
        <label
          >Amount (USD)
          <div class="amount-input">
            <span>$</span><input
              aria-label="Amount (USD)"
              inputmode="decimal"
              bind:value={amount}
              placeholder="0.00"
              required
              autocomplete="off"
            />
          </div></label
        ><label
          >Date<input
            type="date"
            bind:value={date}
            max={familyToday(family?.time_zone)}
            required
          /></label
        >
      </div>
      <p class="field-help">
        $0.00 is welcome for a note. {direction === 'expense'
          ? 'This amount will be subtracted from the bag.'
          : 'This amount will be added to the bag.'}
      </p>
      <label
        >Notes <span class="optional">optional · Markdown supported</span><textarea
          bind:value={notes}
          rows="4"
          maxlength="65536"
          placeholder={direction === 'expense'
            ? 'The weekly grocery shop…'
            : 'A little room for the week ahead…'}></textarea></label
      >
      <label class="file-picker"
        >Attach files <span class="optional">optional · up to 5 MiB each</span><input
          type="file"
          multiple
          on:change={selectFiles}
        /></label
      >
      {#if entry?.attachments?.length || files.length}<ul class="file-list">
          {#each (entry?.attachments || []).filter((file) => !removed.includes(file.id)) as file}<li
            >
              <span>{file.file_name || file.filename}</span><button
                type="button"
                class="text-button danger"
                on:click={() => (removed = [...removed, file.id])}>Remove</button
              >
            </li>{/each}
          {#each files as file, index}<li>
              <span>{file.name} <small class="muted">new</small></span><button
                type="button"
                class="text-button"
                on:click={() => (files = files.filter((_, i) => index !== i))}>Remove</button
              >
            </li>{/each}
        </ul>{/if}
      {#if removed.length}<p class="field-help">
          {removed.length} existing {removed.length === 1 ? 'file will' : 'files will'} be removed when
          you save.
        </p>{/if}
      <div class="form-actions">
        <button type="button" class="secondary" on:click={oncancel} disabled={busy}>Cancel</button
        ><button type="submit" class="primary" disabled={busy || !bag_id}
          >{busy
            ? progress || 'Saving…'
            : entry
              ? 'Save changes'
              : direction === 'expense'
                ? 'Record expense'
                : 'Add money'}</button
        >
      </div>
    </form>
  </dialog>
</div>
