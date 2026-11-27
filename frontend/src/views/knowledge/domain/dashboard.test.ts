import assert from 'node:assert/strict'
import test from 'node:test'
import { dashboardQuery, failedPageJobs, moveCursor } from './dashboard'

test('810 tasks can be navigated without dropping filters or duplicating page jobs', () => {
  const fixture = Array.from({ length: 810 }, (_, i) => ({
    id: String(i + 1), status: i % 2 ? 'failed' : 'succeeded',
  }))
  let cursors = ['']
  const seen = new Set<string>()
  for (let offset = 0; offset < fixture.length; offset += 50) {
    const query = new URLSearchParams(dashboardQuery('', '合成报告', cursors))
    assert.equal(query.get('search'), '合成报告')
    assert.equal(query.get('before'), offset ? String(offset) : '')
    const page = fixture.slice(offset, offset + 50)
    for (const job of page) { assert.ok(!seen.has(job.id)); seen.add(job.id) }
    assert.ok(failedPageJobs(page).every(job => job.status === 'failed'))
    const next = offset + 50 < fixture.length ? page[page.length - 1].id : ''
    cursors = moveCursor(cursors, true, next)
  }
  assert.equal(seen.size, 810)
  assert.equal(cursors.length, 17)
  assert.equal(moveCursor(cursors, false, '').length, 16)
  assert.equal(failedPageJobs(fixture).length, 50)
})
