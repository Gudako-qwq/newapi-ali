import { useEffect, useState } from 'react'

let bundle = null
let generation = 0
export function setSession(value) {
  bundle = value
  generation++
}

export async function api(
  path,
  { method = 'GET', data, headers = {}, signal } = {}
) {
  const version = generation
  const response = await fetch('/api/' + path, {
    method,
    signal,
    credentials: 'same-origin',
    cache: 'no-store',
    headers: {
      Accept: 'application/json',
      ...(method === 'GET'
        ? {}
        : {
            'Content-Type': 'application/json',
            'X-CSRF-Token': bundle?.csrf_token || '',
          }),
      ...headers,
    },
    body: method === 'GET' ? undefined : JSON.stringify(data ?? {}),
  })
  if (version !== generation) throw new DOMException('会话已变更', 'AbortError')
  if (response.status === 401 && bundle)
    window.dispatchEvent(new Event('session-expired'))
  const payload = await response.json()
  if (version !== generation) throw new DOMException('会话已变更', 'AbortError')
  if (!response.ok || !payload.success)
    throw Object.assign(new Error(payload.message || '请求失败，请稍后再试'), {
      status: response.status,
    })
  return payload.data
}

export function useResource(path, revision = 0) {
  const key = `${revision}:${path}`
  const [result, setResult] = useState({})
  useEffect(() => {
    if (!path) return
    const controller = new AbortController()
    api(path, { signal: controller.signal })
      .then((data) => {
        if (!controller.signal.aborted) setResult({ key, data })
      })
      .catch((error) => {
        if (!controller.signal.aborted && error.name !== 'AbortError')
          setResult({ key, error: error.message })
      })
    return () => controller.abort()
  }, [key, path])
  return result.key === key ? result : { loading: Boolean(path) }
}

export const formatTime = (seconds) =>
  seconds
    ? new Date(seconds * 1000).toLocaleString('zh-CN', { hour12: false })
    : '—'
export function channelStatus(row) {
  return (
    {
      creating: '提交中',
      uncertain: '待确认',
      rejected: '被拒绝',
      archived: '已归档',
    }[row.state] ||
    { 1: '已启用', 2: '已停用', 3: '自动停用' }[row.status] ||
    '未知'
  )
}

export function navigate(path, replace = false) {
  window.history[replace ? 'replaceState' : 'pushState']({}, '', path)
  window.dispatchEvent(new Event('popstate'))
}

export const paths = [
  '/channels',
  '/supplier/logs',
  '/customers',
  '/audit',
  '/security',
]
export function safeDestination(path) {
  if (!path || !path.startsWith('/') || path.startsWith('//'))
    return '/channels'
  let url
  try {
    url = new URL(path, window.location.origin)
  } catch {
    return '/channels'
  }
  return url.origin === window.location.origin && paths.includes(url.pathname)
    ? url.pathname + url.search
    : '/channels'
}
