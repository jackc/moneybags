const MAX_CENTS = BigInt(Number.MAX_SAFE_INTEGER);

/** Parse decimal dollars exactly. Never round a fraction or multiply a float. */
export function parseDollars(value, direction = 1) {
  const text = String(value).trim();
  if (!/^\d+(?:\.\d{1,2})?$/.test(text)) {
    throw new Error('Enter a dollar amount with at most two decimal places, such as 12.50.');
  }
  const [whole, fraction = ''] = text.split('.');
  const cents = (BigInt(whole) * 100n + BigInt(fraction.padEnd(2, '0'))) * BigInt(direction);
  if (cents > MAX_CENTS || cents < -MAX_CENTS) throw new Error('That amount is too large.');
  return Number(cents);
}

export function dollarsInput(cents) {
  const value = BigInt(cents ?? 0);
  const absolute = value < 0n ? -value : value;
  return `${absolute / 100n}.${String(absolute % 100n).padStart(2, '0')}`;
}

export function money(cents = 0, signed = false) {
  const value = BigInt(cents);
  const absolute = value < 0n ? -value : value;
  const whole = String(absolute / 100n).replace(/\B(?=(\d{3})+(?!\d))/g, ',');
  return `${value < 0n ? '−' : signed && value > 0n ? '+' : ''}$${whole}.${String(absolute % 100n).padStart(2, '0')}`;
}

export function familyToday(timeZone = 'UTC', now = new Date()) {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit'
  }).formatToParts(now);
  const part = (name) => parts.find((p) => p.type === name).value;
  return `${part('year')}-${part('month')}-${part('day')}`;
}

export function dateLabel(date) {
  if (!date) return '';
  return new Intl.DateTimeFormat('en-US', {
    month: 'short',
    day: 'numeric',
    year: 'numeric',
    timeZone: 'UTC'
  }).format(new Date(`${date.slice(0, 10)}T12:00:00Z`));
}
