<script>
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import { action, friendlyError } from '$lib/api.js';
  export let family;
  let authorization = null,
    error = '',
    busy = false;
  $: params = Object.fromEntries($page.url.searchParams.entries());
  const permissions = {
    'bags:read': 'Read your family’s bags, balances, entries, and attachments.',
    'bags:write': 'Create and manage bags, entries, notes, and attachments.',
    'family:write': 'Manage family settings, invitations, and account deletion.'
  };
  onMount(async () => {
    try {
      authorization = await action('prepare_oauth_authorization', params);
    } catch (e) {
      error = friendlyError(e);
    }
  });
  async function approve() {
    busy = true;
    error = '';
    try {
      const result = await action('create_oauth_authorization_code', params);
      const target = new URL(result.redirect_uri);
      target.searchParams.set('code', result.code);
      if (result.state !== undefined) target.searchParams.set('state', result.state);
      location.assign(target.href);
    } catch (e) {
      error = friendlyError(e);
      busy = false;
    }
  }
</script>

<section class="settings-card consent-card">
  <p class="eyebrow">YOU'RE IN CONTROL</p>
  <h1>Connect to your bags</h1>
  {#if error}<div class="notice error" role="alert">{error}</div>{/if}{#if authorization}<p>
      <strong
        >{authorization.client?.client_name ||
          authorization.client?.name ||
          params.client_id}</strong
      >
      would like to access <strong>{family.name}</strong> through your account.
    </p>
    <ul class="scope-list">
      {#each Array.isArray(authorization.scope) ? authorization.scope : authorization.scope?.split(' ') || [] as scope}<li
        >
          <span aria-hidden="true">✓</span>{permissions[scope] || scope}
        </li>{/each}
    </ul>
    <p class="field-help">
      This includes data shared by everyone in your family. You can revoke this connection in
      Settings at any time.
    </p>
    <div class="form-actions">
      <a class="secondary button-link" href="/">Cancel</a><button
        class="primary"
        disabled={busy}
        on:click={approve}>{busy ? 'Connecting…' : 'Allow connection'}</button
      >
    </div>{:else if !error}<p class="muted">Checking the connection request…</p>{/if}
</section>
