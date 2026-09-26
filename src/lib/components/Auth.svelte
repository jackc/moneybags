<script>
  import { onMount } from 'svelte';
  import { action, friendlyError, publicSettings } from '$lib/api.js';
  import { passkeyLogin, supportsPasskeys } from '$lib/passkeys.js';
  import BagIcon from './BagIcon.svelte';
  export let onlogin;
  export let invitation = '';
  let mode = 'login';
  let allowRegistration = false;
  onMount(async () => {
    try {
      const settings = await publicSettings();
      allowRegistration = settings.allow_registration === true;
    } catch {
      // Keep sign-in and invitations available if settings cannot be loaded.
    }
  });
  let username = '',
    password = '',
    display_name = '',
    family_name = '',
    time_zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  let busy = false,
    error = '';
  $: registering = (allowRegistration && mode === 'register') || !!invitation;
  async function submit() {
    busy = true;
    error = '';
    try {
      const result = await action(
        invitation ? 'accept_invitation' : registering ? 'register' : 'login',
        {
          username,
          password,
          ...(registering ? { display_name } : {}),
          ...(invitation ? { token: invitation } : registering ? { family_name, time_zone } : {})
        }
      );
      await onlogin(result);
    } catch (e) {
      error = friendlyError(e);
    } finally {
      busy = false;
    }
  }
  async function loginWithPasskey() {
    busy = true;
    error = '';
    try {
      await onlogin(await passkeyLogin());
    } catch (e) {
      error = friendlyError(e);
    } finally {
      busy = false;
    }
  }
</script>

<div class="auth-layout">
  <section class="auth-story">
    <div class="brand">
      <BagIcon size={34} /><span>money bags<span class="brand-dot">.</span></span>
    </div>
    <div>
      <p class="eyebrow">A LITTLE CLARITY GOES A LONG WAY</p>
      <h1>Room for what<br />matters.</h1>
      <p class="story-copy">
        Your family's spending, in simple bags.<br />Know what's left. Get on with your day.
      </p>
    </div>
    <div class="sample-bag" aria-hidden="true">
      <span class="sample-icon"><BagIcon size={38} /></span>
      <div><small>Groceries</small><strong>$870.18</strong><span>left for the everyday</span></div>
      <span class="sample-check">✓</span>
    </div>
    <p class="story-footer">A shared picture. A little more peace of mind.</p>
  </section>
  <section class="auth-panel">
    <div class="auth-form">
      <p class="eyebrow">
        {invitation ? 'BETTER TOGETHER' : registering ? 'YOUR FRESH START' : 'WELCOME BACK'}
      </p>
      <h2>
        {invitation ? 'Join your family' : registering ? 'Make room for a plan' : 'Open your bags'}
      </h2>
      <p class="muted">
        {registering
          ? 'Create your own login. Everyone in a family shares the same access.'
          : 'Sign in to see where things stand.'}
      </p>
      {#if error}<div class="notice error" role="alert">{error}</div>{/if}
      <form on:submit|preventDefault={submit}>
        {#if registering}<label
            >Your name<input
              bind:value={display_name}
              autocomplete="name"
              required
              maxlength="120"
              placeholder="Alex"
            /></label
          >{/if}
        <label
          >Username<input
            bind:value={username}
            autocomplete="username"
            required
            maxlength="120"
            autocapitalize="none"
            spellcheck="false"
          /></label
        >
        <label
          >Password<input
            type="password"
            bind:value={password}
            autocomplete={registering ? 'new-password' : 'current-password'}
            minlength={registering ? 12 : undefined}
            required
          />{#if registering}<span class="field-help">At least 12 characters.</span>{/if}</label
        >
        {#if registering && !invitation}
          <label
            >Family name<input
              bind:value={family_name}
              required
              maxlength="120"
              placeholder="Our household"
            /></label
          >
          <label
            >Family time zone<input
              bind:value={time_zone}
              required
              placeholder="America/Chicago"
            /><span class="field-help"
              >Sets today's date for entries. A family of one is welcome, too.</span
            ></label
          >
        {/if}
        <button class="primary full" disabled={busy} type="submit"
          >{busy ? 'One moment…' : registering ? 'Create account' : 'Sign in'}<span
            aria-hidden="true">→</span
          ></button
        >
      </form>
      {#if !registering && supportsPasskeys()}
        <div class="divider"><span>or</span></div>
        <button class="secondary full" disabled={busy} on:click={loginWithPasskey}
          >Sign in with a passkey</button
        >
      {/if}
      {#if !invitation && allowRegistration}<p class="auth-switch">
          {registering ? 'Already have an account?' : 'A new beginning?'}
          <button
            class="text-button"
            on:click={() => {
              mode = registering ? 'login' : 'register';
              error = '';
            }}>{registering ? 'Sign in' : 'Create an account'}</button
          >
        </p>{/if}
    </div>
  </section>
</div>
