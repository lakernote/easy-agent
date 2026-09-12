import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import { transform } from 'esbuild'

const source = await readFile(new URL('../src/api.ts', import.meta.url), 'utf8')
const compiled = await transform(source, { format: 'esm', loader: 'ts', target: 'es2022' })
const moduleURL = `data:text/javascript;base64,${Buffer.from(compiled.code).toString('base64')}`
const { APIError, api, onAuthenticationRequired } = await import(moduleURL)

test('notifies the app when a protected endpoint requires authentication', async (context) => {
  const originalFetch = globalThis.fetch
  context.after(() => { globalThis.fetch = originalFetch })
  globalThis.fetch = async () => new Response(JSON.stringify({
    code: 'authentication_required',
    error: '需要登录',
  }), { status: 401, headers: { 'Content-Type': 'application/json' } })

  let notifications = 0
  const unsubscribe = onAuthenticationRequired(() => { notifications += 1 })
  context.after(unsubscribe)

  await assert.rejects(api.bootstrap(), (error) => {
    assert.ok(error instanceof APIError)
    assert.equal(error.status, 401)
    assert.equal(error.code, 'authentication_required')
    return true
  })
  assert.equal(notifications, 1)
})

test('does not treat invalid credentials as an expired session', async (context) => {
  const originalFetch = globalThis.fetch
  context.after(() => { globalThis.fetch = originalFetch })
  globalThis.fetch = async () => new Response(JSON.stringify({
    error: '用户名或密码不正确',
  }), { status: 401, headers: { 'Content-Type': 'application/json' } })

  let notifications = 0
  const unsubscribe = onAuthenticationRequired(() => { notifications += 1 })
  context.after(unsubscribe)

  await assert.rejects(api.login('admin', 'wrong-password'), (error) => {
    assert.ok(error instanceof APIError)
    assert.equal(error.status, 401)
    assert.equal(error.code, '')
    return true
  })
  assert.equal(notifications, 0)
})
