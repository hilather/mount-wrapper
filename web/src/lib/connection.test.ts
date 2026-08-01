import { describe, expect, it } from 'vitest'
import { connectionLabel, connectionTitle } from './connection'

describe('connectionLabel', () => {
  it('shows live (SSE) when connected and SSE is open', () => {
    expect(connectionLabel({ status: 'connected', sseActive: true })).toBe('live (SSE)')
  })

  it('shows poll (SSE down) when connected without SSE', () => {
    expect(connectionLabel({ status: 'connected', sseActive: false })).toBe('poll (SSE down)')
  })

  it('shows reconnecting', () => {
    expect(connectionLabel({ status: 'reconnecting', sseActive: false })).toBe('reconnecting')
  })

  it('shows service down vs error for service-down status', () => {
    expect(connectionLabel({ status: 'service-down', sseActive: false })).toBe('service down')
    expect(
      connectionLabel({ status: 'service-down', sseActive: false, errorKind: 'service-down' }),
    ).toBe('service down')
    expect(connectionLabel({ status: 'service-down', sseActive: false, errorKind: 'error' })).toBe(
      'error',
    )
  })

  it('shows ellipsis for unknown', () => {
    expect(connectionLabel({ status: 'unknown', sseActive: false })).toBe('…')
  })
})

describe('connectionTitle', () => {
  it('describes SSE vs poll when connected', () => {
    expect(connectionTitle({ status: 'connected', sseActive: true })).toMatch(/Server-Sent Events/i)
    expect(connectionTitle({ status: 'connected', sseActive: false })).toMatch(/Polling|SSE is down/i)
  })

  it('describes reconnecting and service-down', () => {
    expect(connectionTitle({ status: 'reconnecting', sseActive: false })).toMatch(/reconnect/i)
    expect(connectionTitle({ status: 'service-down', sseActive: false })).toMatch(/not reachable/i)
    expect(
      connectionTitle({ status: 'service-down', sseActive: false, errorKind: 'error' }),
    ).toMatch(/network or auth/i)
  })

  it('describes unknown', () => {
    expect(connectionTitle({ status: 'unknown', sseActive: false })).toMatch(/unknown/i)
  })
})
