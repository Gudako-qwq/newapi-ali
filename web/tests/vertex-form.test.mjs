import assert from 'node:assert/strict'
import test from 'node:test'

import {
  defaultVertexTemplate,
  parseModelNames,
  regionError,
  validateVertex,
} from '../src/portal/vertex-form.js'

const template = {
  type: 41,
  id: 'vertex-key',
  credential_kind: 'api_key',
  regions: ['global', 'us-central1'],
  models: ['alpha', 'beta'],
}
const service = {
  ...template,
  id: 'vertex-json',
  credential_kind: 'vertex_json',
}
const input = {
  template,
  name: 'test',
  credential: 'test-key-1234',
  project: '',
  region: 'global',
  models: ['alpha'],
}

test('one provider supports either credential format; service account is the native default', () => {
  assert.equal(defaultVertexTemplate([template, service]).id, 'vertex-json')
  assert.equal(defaultVertexTemplate([template]).id, 'vertex-key')
  assert.deepEqual(validateVertex(input), {})
  assert.deepEqual(
    Object.keys(
      validateVertex({ ...input, name: ' ', credential: '', models: [] })
    ).sort(),
    ['credential', 'models', 'name']
  )
  assert.ok(
    validateVertex({ ...input, template: { ...template, type: 1 } }).type
  )
  assert.ok(
    validateVertex({ ...input, credential: 'first-key\nsecond-key' }).credential
  )
  assert.ok(validateVertex({ ...input, project: 'not a project' }).project)
  assert.deepEqual(validateVertex({ ...input, models: ['custom-model'] }), {})
})

test('manual model entry preserves order and case, ignores empty items and deduplicates', () => {
  assert.deepEqual(
    parseModelNames('  model-A, model-B，model-A\r\nmodel-b\n\n'),
    ['model-A', 'model-B', 'model-b']
  )
  assert.deepEqual(parseModelNames(' , ，\n '), [])
  const models = parseModelNames(
    'publishers/google/models/custom-v1\npartner.model@20261126'
  )
  const region = '{"default":"global","partner.model@20261126":"us-central1"}'
  assert.deepEqual(validateVertex({ ...input, models, region }), {})
})

test('manual model IDs are bounded and cannot encode URLs or additional wire IDs', () => {
  for (const models of [
    [],
    ['a b'],
    ['a,b'],
    ['a\nb'],
    ['a/../b'],
    ['https://example.invalid'],
    ['wild*'],
    ['a'.repeat(257)],
    Array.from({ length: 101 }, (_, index) => 'model-' + index),
  ]) {
    assert.ok(validateVertex({ ...input, models }).models)
  }
  assert.deepEqual(validateVertex({ ...input, models: ['a'.repeat(256)] }), {})
})

test('JSON and API Key are distinct validation paths', () => {
  assert.ok(validateVertex({ ...input, template: service }).credential)
  const credential = JSON.stringify({
    type: 'service_account',
    client_email: 'test@test.iam.gserviceaccount.com',
    project_id: 'demo-project',
    private_key: '-----BEGIN PRIVATE KEY-----test',
    token_uri: 'https://oauth2.googleapis.com/token',
  })
  assert.deepEqual(
    validateVertex({ ...input, template: service, credential }),
    {}
  )
})

test('region supports an allowed name or per-selected-model JSON, never a URL', () => {
  assert.equal(regionError('us-central1', template, ['alpha']), '')
  assert.equal(
    regionError('{"default":"global","alpha":"us-central1"}', template, [
      'alpha',
    ]),
    ''
  )
  for (const value of [
    '',
    'https://example.invalid',
    '{"default":"other"}',
    '{"default":"global","beta":"global"}',
    '{"alpha":"global"}',
    '{bad',
  ])
    assert.ok(regionError(value, template, ['alpha']))
})
