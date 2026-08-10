import assert from 'node:assert/strict';
import test from 'node:test';

import { prometheusSamples, safeMarkdownText } from '../src/shared-logic.js';

test('sanitizes HTML, scripts, dangerous links, and external images', () => {
  const result = safeMarkdownText('<script>alert(1)</script> [x](javascript:alert(1)) ![track](https://x/p.png)');
  assert.equal(result.includes('<script>'), false);
  assert.equal(result.toLowerCase().includes('javascript:'), false);
  assert.equal(result.includes('https://x'), false);
  assert.match(result, /外链图片已隐藏/);
});

test('parses bounded Prometheus samples', () => {
  assert.deepEqual(prometheusSamples({ data: { result: [{ metric: { node: 'game@127.0.0.1' }, value: [1785823200, '1'] }] } }), [{
    metric: { node: 'game@127.0.0.1' }, value: 1, sampledAt: 1785823200000,
  }]);
  assert.deepEqual(prometheusSamples({ data: { result: [{ value: ['bad', 'bad'] }] } }), []);
});
