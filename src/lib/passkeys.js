import {
  startAuthentication,
  startRegistration,
  browserSupportsWebAuthn
} from '@simplewebauthn/browser';
import { action } from './api.js';

export const supportsPasskeys = () => browserSupportsWebAuthn();
export async function passkeyLogin() {
  const { options, challenge_id } = await action('begin_passkey_login');
  const credential = await startAuthentication({ optionsJSON: options.publicKey ?? options });
  return action('finish_passkey_login', { challenge_id, credential });
}
export async function enrollPasskey(name) {
  const { options, challenge_id } = await action('begin_passkey_registration');
  const credential = await startRegistration({ optionsJSON: options.publicKey ?? options });
  return action('finish_passkey_registration', { challenge_id, credential, name });
}
