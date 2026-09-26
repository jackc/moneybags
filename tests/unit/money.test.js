import test from 'node:test';
import assert from 'node:assert/strict';
import { parseDollars, money, dollarsInput, familyToday } from '../../src/lib/money.js';
import { renderNotes } from '../../src/lib/markdown.js';

test('decimal input preserves exact cents, signs, zero, and safe integer limits', () => {
  for (const [input, expected] of [
    ['0', 0],
    ['0.00', 0],
    ['0.29', 29],
    ['87.32', 8732],
    ['1.1', 110],
    ['90071992547409.91', Number.MAX_SAFE_INTEGER]
  ]) {
    assert.equal(parseDollars(input), expected);
    assert.equal(parseDollars(input, -1), expected === 0 ? 0 : -expected);
  }
  for (const input of [
    '',
    ' ',
    '-1',
    '1e3',
    '1,000',
    'NaN',
    'Infinity',
    '1.001',
    '90071992547409.92'
  ])
    assert.throws(() => parseDollars(input));
});

test('display and edit round-trip without floating point arithmetic', () => {
  assert.equal(money(-8732), '−$87.32');
  assert.equal(money(91268), '$912.68');
  assert.equal(money(0, true), '$0.00');
  assert.equal(money(123, true), '+$1.23');
  assert.equal(money(9007199254740991n), '$90,071,992,547,409.91');
  assert.equal(dollarsInput(-9007199254740991), '90071992547409.91');
  assert.equal(parseDollars(dollarsInput(Number.MAX_SAFE_INTEGER)), Number.MAX_SAFE_INTEGER);
});

test('entry default date uses family time zone instead of UTC or browser time zone', () => {
  const now = new Date('2026-09-27T01:00:00Z');
  assert.equal(familyToday('America/Chicago', now), '2026-09-26');
  assert.equal(familyToday('Asia/Tokyo', now), '2026-09-27');
});

test('Markdown supports tables without trusting HTML, handlers, or unsafe links', () => {
  const result = renderNotes(
    '<img src=x onerror=alert(1)>\n\n<script>alert(1)</script>\n\n[bad](javascript:alert(1))\n\n| Item | Cost |\n| --- | ---: |\n| Milk | $9 |'
  );
  assert.ok(result.includes('<table>'));
  assert.ok(result.includes('&lt;script&gt;'));
  assert.ok(!result.includes('<script>'));
  assert.ok(!result.includes('<img src=x'));
  assert.ok(!result.includes('href="javascript:'));
});
