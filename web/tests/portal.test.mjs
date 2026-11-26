import assert from 'node:assert/strict'
import test from 'node:test'

import {
  api,
  channelStatus,
  newSubmissionKey,
  safeDestination,
  setSession,
} from '../src/portal/api.js'

test('public HTTP can generate submission keys without crypto.randomUUID', () => {
  const httpCrypto = {
    getRandomValues: (bytes) => globalThis.crypto.getRandomValues(bytes),
  }
  const key = newSubmissionKey(httpCrypto)
  assert.match(key, /^[0-9a-f]{32}$/)
  assert.notEqual(newSubmissionKey(httpCrypto), key)
  assert.throws(() => newSubmissionKey({}), /无法生成安全的提交标识/)
})

test('login redirect stays on the local console and its known routes', () => {
  globalThis.window = { location: { origin: 'https://portal.example' } }
  for (const path of [
    '//example.org',
    '/\\example.org',
    'https://example.org',
    '/api/admin/users',
    '/unknown',
  ]) {
    assert.equal(safeDestination(path), '/channels')
  }
  assert.equal(
    safeDestination('/supplier/logs?channel=7'),
    '/supplier/logs?channel=7'
  )
})

test('unknown creation outcomes are not displayed as active channels', () => {
  assert.equal(channelStatus({ state: 'uncertain', status: 1 }), '待确认')
  assert.equal(channelStatus({ state: 'ready', status: 2 }), '已停用')
  assert.equal(channelStatus({ state: 'archived', status: 0 }), '已归档')
})

test('create uses local CSRF and idempotency; late responses do not cross sessions', async (t) => {
  const original = globalThis.fetch
  t.after(() => {
    globalThis.fetch = original
    setSession(null)
  })
  setSession({ csrf_token: 'test-csrf' })
  let resolveFetch
  globalThis.fetch = (path, options) => {
    assert.equal(path, '/api/channels')
    assert.equal(options.credentials, 'same-origin')
    assert.equal(options.headers['X-CSRF-Token'], 'test-csrf')
    assert.equal(options.headers['Idempotency-Key'], 'unit-key')
    return new Promise((resolve) => {
      resolveFetch = resolve
    })
  }
  const pending = api('channels', {
    method: 'POST',
    data: { name: 'test' },
    headers: { 'Idempotency-Key': 'unit-key' },
  })
  setSession(null)
  resolveFetch(Response.json({ success: true, data: { id: 1 } }))
  await assert.rejects(pending, { name: 'AbortError' })
})
