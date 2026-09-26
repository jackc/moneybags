<script>
  import { openDialog } from '$lib/dialog.js';
  import { onMount } from 'svelte';
  import { action, mutation, items, requestID, friendlyError } from '$lib/api.js';
  import { enrollPasskey, supportsPasskeys } from '$lib/passkeys.js';
  export let user;
  export let family;
  export let onfamily;
  export let ondeleted;
  let tab = 'family',
    members = [],
    invitations = [],
    connections = [],
    passkeys = [];
  let error = '',
    notice = '',
    busy = false,
    loading = true,
    invitationURL = '';
  let name = family?.name || '',
    time_zone = family?.time_zone || 'UTC';
  let current_password = '',
    new_password = '',
    passkey_name = '';
  let confirmUser = null,
    confirmPasskey = null,
    confirmation = '',
    stepup = '';
  let deleteRequest = '';
  let confirmEnrollment = false;
  const endpoint = `${location.origin}/mcp`;

  async function all(name, key) {
    let result = [],
      cursor = '';
    do {
      const page = await action(name, { limit: 100, ...(cursor ? { cursor } : {}) });
      result = [...result, ...items(page, key)];
      cursor = page.next_cursor || '';
    } while (cursor);
    return result;
  }
  async function refresh() {
    loading = true;
    try {
      [members, invitations, connections, passkeys] = await Promise.all([
        all('list_users', 'users'),
        all('list_invitations', 'invitations'),
        all('list_connections', 'connections'),
        all('list_passkeys', 'passkeys')
      ]);
    } catch (e) {
      error = friendlyError(e);
    } finally {
      loading = false;
    }
  }
  onMount(refresh);
  async function run(fn, message) {
    busy = true;
    error = '';
    notice = '';
    try {
      await fn();
      notice = message;
      await refresh();
    } catch (e) {
      error = friendlyError(e);
    } finally {
      busy = false;
    }
  }
  async function saveFamily() {
    await run(async () => {
      const result = await mutation('update_family', {
        name,
        time_zone,
        expected_version: family.version
      });
      const updated = result.family || result;
      onfamily(updated);
    }, 'Family settings updated.');
  }
  async function invite() {
    await run(async () => {
      const result = await mutation('create_invitation');
      if (!result.url && !result.token)
        throw new Error(
          'The invitation was created, but its private link was not received. Revoke it and create a new invitation.'
        );
      invitationURL = result.url
        ? new URL(result.url, location.origin).href
        : `${location.origin}/invite/${encodeURIComponent(result.token)}`;
    }, 'Invitation ready to share.');
  }
  async function copy(text) {
    try {
      await navigator.clipboard.writeText(text);
      notice = 'Copied to clipboard.';
    } catch {
      notice = 'Select and copy the link below.';
    }
  }
  async function deleteAccount() {
    await run(async () => {
      const id = confirmUser.id;
      await action('delete_user', { user_id: id, request_id: deleteRequest });
      confirmUser = null;
      if (id === user.id) await ondeleted();
    }, 'Account deleted. Shared entry history is preserved.');
  }
  async function removePasskey() {
    await run(async () => {
      await action('reauthenticate', { password: stepup });
      await action('delete_passkey', { passkey_id: confirmPasskey.id });
      confirmPasskey = null;
      stepup = '';
    }, 'Passkey removed.');
  }
  async function addPasskey() {
    busy = true;
    error = '';
    try {
      await enrollPasskey(passkey_name || 'My passkey');
      passkey_name = '';
      notice = 'Passkey added.';
      await refresh();
    } catch (e) {
      if (e.code === 'recent_authentication_required') {
        stepup = '';
        confirmEnrollment = true;
      } else error = friendlyError(e);
    } finally {
      busy = false;
    }
  }
  async function confirmAndEnroll() {
    busy = true;
    error = '';
    try {
      await action('reauthenticate', { password: stepup });
      stepup = '';
      confirmEnrollment = false;
      await addPasskey();
    } catch (e) {
      error = friendlyError(e);
    } finally {
      busy = false;
    }
  }
</script>

<div class="page-heading">
  <div>
    <p class="eyebrow">THE DETAILS, TAKEN CARE OF</p>
    <h1>Make yourself at home.</h1>
    <p class="muted">Your family, your account, and the tools you connect.</p>
  </div>
</div>
<div class="tabs settings-tabs">
  <button class:active={tab === 'family'} on:click={() => (tab = 'family')}>Family</button><button
    class:active={tab === 'account'}
    on:click={() => (tab = 'account')}>Account & security</button
  ><button class:active={tab === 'connections'} on:click={() => (tab = 'connections')}
    >Connected clients</button
  >
</div>
{#if error}<div class="notice error" role="alert">{error}</div>{/if}
{#if notice}<div class="notice success" role="status">{notice}</div>{/if}
{#if loading}<p class="muted" aria-live="polite">Loading settings…</p>{/if}
{#if tab === 'family'}
  <section class="settings-card">
    <h2>Your family</h2>
    <p class="muted">Every member can manage all bags and family settings.</p>
    <form on:submit|preventDefault={saveFamily}>
      <div class="form-grid">
        <label>Family name<input bind:value={name} required maxlength="120" /></label><label
          >Time zone<input
            bind:value={time_zone}
            required
            placeholder="America/Chicago"
            aria-describedby="time-zone-help"
          /><span id="time-zone-help" class="field-help"
            >Entry dates default to today in this time zone.</span
          ></label
        >
      </div>
      <button class="primary" disabled={busy}>Save family settings</button>
    </form>
  </section>
  <section class="settings-card">
    <div class="section-heading">
      <div>
        <h2>Family members</h2>
        <p class="muted">Individual logins. One shared picture.</p>
      </div>
      <button class="secondary" disabled={busy} on:click={invite}>+ Invite someone</button>
    </div>
    {#if invitationURL}<div class="invitation-result">
        <label>Share this single-use invitation<input readonly value={invitationURL} /></label
        ><button class="secondary" on:click={() => copy(invitationURL)}>Copy invitation</button>
        <p class="field-help">
          The link expires. Share it directly with the person you want to invite.
        </p>
      </div>{/if}
    <ul class="management-list">
      {#each members as member}<li>
          <span class="member-avatar"
            >{(member.display_name || member.username || '?').slice(0, 1).toUpperCase()}</span
          >
          <div class="grow">
            <strong
              >{member.display_name || member.username}{member.id === user.id
                ? ' (you)'
                : ''}</strong
            ><small>@{member.username}</small>
          </div>
          <button
            class="text-button danger"
            on:click={() => {
              confirmUser = member;
              confirmation = '';
              deleteRequest = requestID();
            }}>Delete account</button
          >
        </li>{/each}
    </ul>
  </section>
  <section class="settings-card">
    <h2>Invitations</h2>
    {#if !invitations.length}<p class="muted">No invitations yet.</p>{:else}<ul
        class="management-list"
      >
        {#each invitations as invitation}<li>
            <div class="grow">
              <strong
                >{invitation.accepted_at || invitation.accepted_by || invitation.accepted
                  ? 'Accepted'
                  : invitation.revoked_at || invitation.revoked
                    ? 'Revoked'
                    : new Date(invitation.expires_at) < new Date()
                      ? 'Expired'
                      : 'Pending invitation'}</strong
              ><small>Expires {new Date(invitation.expires_at).toLocaleString()}</small>
            </div>
            {#if !invitation.accepted_at && !invitation.accepted_by && !invitation.accepted && !invitation.revoked_at && !invitation.revoked}<button
                class="text-button danger"
                disabled={busy}
                on:click={() =>
                  run(
                    () => mutation('revoke_invitation', { invitation_id: invitation.id }),
                    'Invitation revoked.'
                  )}>Revoke</button
              >{/if}
          </li>{/each}
      </ul>{/if}
  </section>
{:else if tab === 'account'}
  <section class="settings-card">
    <h2>Your account</h2>
    <p>{user.display_name || user.username} <span class="muted">· @{user.username}</span></p>
  </section>
  <section class="settings-card">
    <h2>Passkeys</h2>
    <p class="muted">
      Sign in with your device's fingerprint, face, or security key. You can keep more than one.
    </p>
    <ul class="management-list">
      {#each passkeys as passkey}<li>
          <div class="grow">
            <strong>{passkey.name || passkey.description || 'Passkey'}</strong><small
              >Added {new Date(passkey.created_at).toLocaleDateString()}</small
            >
          </div>
          <button
            class="text-button danger"
            on:click={() => {
              confirmPasskey = passkey;
              stepup = '';
            }}>Remove</button
          >
        </li>{/each}
    </ul>
    {#if !passkeys.length}<p class="field-help">
        No passkeys added yet. Your password is available as another way to sign in.
      </p>{/if}
    {#if supportsPasskeys()}<form class="inline-form" on:submit|preventDefault={addPasskey}>
        <label
          >Passkey name<input
            bind:value={passkey_name}
            maxlength="120"
            placeholder="My phone"
          /></label
        ><button class="primary" disabled={busy}>Add passkey</button>
      </form>{:else}<p class="notice">
        Passkeys require a supported browser and a secure connection.
      </p>{/if}
  </section>
  <section class="settings-card">
    <h2>Change password</h2>
    <form
      on:submit|preventDefault={() =>
        run(async () => {
          await action('change_password', { current_password, new_password });
          current_password = '';
          new_password = '';
        }, 'Password changed.')}
    >
      <div class="form-grid">
        <label
          >Current password<input
            type="password"
            bind:value={current_password}
            autocomplete="current-password"
            required
          /></label
        ><label
          >New password<input
            type="password"
            bind:value={new_password}
            autocomplete="new-password"
            minlength="12"
            required
          /><span class="field-help">At least 12 characters.</span></label
        >
      </div>
      <button class="primary" disabled={busy}>Change password</button>
    </form>
  </section>
{:else}
  <section class="settings-card">
    <h2>Your assistant, connected</h2>
    <p class="muted">
      Connect a compatible MCP client to read balances or record spending on your behalf. You choose
      what each connection can access.
    </p>
    <label>Server URL<input readonly value={endpoint} /></label><button
      class="secondary"
      on:click={() => copy(endpoint)}>Copy server URL</button
    >
  </section>
  <section class="settings-card">
    <h2>Connected clients</h2>
    <p class="field-help">
      Connections belong to your account. Revoking one immediately removes its access.
    </p>
    {#if !connections.length}<div class="empty-inline">No clients connected yet.</div>{:else}<ul
        class="management-list"
      >
        {#each connections as connection}<li>
            <div class="grow">
              <strong
                >{connection.client_name ||
                  connection.client?.client_name ||
                  connection.client_id ||
                  'Connected client'}</strong
              ><small
                >{Array.isArray(connection.scopes)
                  ? connection.scopes.join(', ')
                  : connection.scope || ''}</small
              >
            </div>
            <button
              class="text-button danger"
              disabled={busy}
              on:click={() =>
                run(
                  () => mutation('revoke_connection', { connection_id: connection.id }),
                  'Connection revoked.'
                )}>Revoke access</button
            >
          </li>{/each}
      </ul>{/if}
  </section>
{/if}

{#if confirmUser}<div class="sheet-backdrop" role="presentation">
    <dialog
      class="sheet small"
      aria-labelledby="account-delete-title"
      use:openDialog={() => {
        if (!busy) confirmUser = null;
      }}
    >
      <h2 id="account-delete-title">
        Delete {confirmUser.id === user.id
          ? 'your'
          : `${confirmUser.display_name || confirmUser.username}'s`} account?
      </h2>
      <p>
        This permanently deletes the login, passkeys, and connected-client access. Entries and
        attachments stay with the family, including their historical author names.
      </p>
      {#if confirmUser.id === user.id}<p>You will be signed out immediately.</p>{/if}<label
        >Type DELETE to confirm<input bind:value={confirmation} autocomplete="off" /></label
      >{#if error}<div class="notice error" role="alert">{error}</div>{/if}
      <div class="form-actions">
        <button class="secondary" disabled={busy} on:click={() => (confirmUser = null)}
          >Cancel</button
        ><button
          class="danger-button"
          disabled={busy || confirmation !== 'DELETE'}
          on:click={deleteAccount}>Delete account</button
        >
      </div>
    </dialog>
  </div>{/if}
{#if confirmEnrollment}
  <div class="sheet-backdrop" role="presentation">
    <dialog
      class="sheet small"
      aria-labelledby="enroll-title"
      use:openDialog={() => {
        if (!busy) confirmEnrollment = false;
      }}
    >
      <h2 id="enroll-title">Confirm it's you</h2>
      <p>Enter your password before adding a new passkey.</p>
      <form on:submit|preventDefault={confirmAndEnroll}>
        <label
          >Password<input
            type="password"
            bind:value={stepup}
            autocomplete="current-password"
            required
          /></label
        >
        {#if error}<div class="notice error" role="alert">{error}</div>{/if}
        <div class="form-actions">
          <button
            type="button"
            class="secondary"
            disabled={busy}
            on:click={() => (confirmEnrollment = false)}>Cancel</button
          ><button class="primary" disabled={busy}>Continue</button>
        </div>
      </form>
    </dialog>
  </div>
{/if}
{#if confirmPasskey}<div class="sheet-backdrop" role="presentation">
    <dialog
      class="sheet small"
      aria-labelledby="passkey-delete-title"
      use:openDialog={() => {
        if (!busy) confirmPasskey = null;
      }}
    >
      <h2 id="passkey-delete-title">Remove passkey</h2>
      <p>Confirm your password to remove “{confirmPasskey.name || 'this passkey'}”.</p>
      <form on:submit|preventDefault={removePasskey}>
        <label
          >Password<input
            type="password"
            bind:value={stepup}
            autocomplete="current-password"
            required
          /></label
        >{#if error}<div class="notice error" role="alert">{error}</div>{/if}
        <div class="form-actions">
          <button
            type="button"
            class="secondary"
            disabled={busy}
            on:click={() => (confirmPasskey = null)}>Cancel</button
          ><button class="danger-button" disabled={busy}>Remove passkey</button>
        </div>
      </form>
    </dialog>
  </div>{/if}
