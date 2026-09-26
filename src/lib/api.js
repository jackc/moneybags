export class APIError extends Error {
  constructor(message, status, code) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

async function responseData(response) {
  const data = await response.json().catch(() => ({}));
  if (!response.ok || data.error) {
    const error = data.error;
    throw new APIError(
      typeof error === 'string'
        ? error
        : error?.message || data.message || 'The request could not be completed.',
      response.status,
      error?.code || data.code
    );
  }
  return data.result ?? data;
}

export async function action(name, params = {}) {
  return responseData(
    await fetch(`/api/actions/${name}`, {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(params)
    })
  );
}

export const requestID = () => crypto.randomUUID();
export const items = (result, key) =>
  result?.[key] ?? result?.items ?? (Array.isArray(result) ? result : []);

const pendingMutations = new Map();
/** Keep an unanswered operation's request key for a safe, unchanged retry. */
export async function mutation(name, params = {}) {
  const signature = JSON.stringify([name, params]);
  if (!pendingMutations.has(signature)) pendingMutations.set(signature, requestID());
  const result = await action(name, { ...params, request_id: pendingMutations.get(signature) });
  pendingMutations.delete(signature);
  return result;
}

export async function stageFile(file, request_id) {
  if (file.size > 5 * 1024 * 1024)
    throw new Error(`${file.name} is larger than the 5 MiB file limit.`);
  const form = new FormData();
  form.append('file', file);
  form.append('request_id', request_id);
  return responseData(
    await fetch('/api/uploads', { method: 'POST', credentials: 'same-origin', body: form })
  );
}

export function friendlyError(error) {
  if (error.code === 'version_conflict')
    return 'Someone changed this item. Refresh the page and review the latest version before saving.';
  if (error.code === 'bag_archived')
    return 'This bag is archived. Unarchive it before adding or editing entries.';
  return error.message || 'Something went wrong. Please try again.';
}
