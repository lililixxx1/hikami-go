import { describe, it, expect } from 'vitest'
import { formatDateTime, formatDate, formatFileSize, formatDuration } from './format'

describe('formatDateTime', () => {
  it('returns "-" for empty string', () => {
    expect(formatDateTime('')).toBe('-')
  })

  it('formats ISO date string', () => {
    const result = formatDateTime('2026-06-04T12:30:45Z')
    expect(result).toContain('2026')
    expect(result).toContain('30')
    expect(result).toContain('45')
  })

  it('returns raw value for invalid date', () => {
    expect(formatDateTime('not-a-date')).toBe('not-a-date')
  })
})

describe('formatDate', () => {
  it('returns "-" for empty string', () => {
    expect(formatDate('')).toBe('-')
  })

  it('formats ISO date string', () => {
    const result = formatDate('2026-06-04T00:00:00Z')
    expect(result).toContain('2026')
    expect(result).toContain('06')
  })

  it('returns raw value for invalid date', () => {
    expect(formatDate('garbage')).toBe('garbage')
  })
})

describe('formatFileSize', () => {
  it('returns "-" for negative bytes', () => {
    expect(formatFileSize(-1)).toBe('-')
  })

  it('returns "0 B" for zero', () => {
    expect(formatFileSize(0)).toBe('0 B')
  })

  it('formats bytes without decimal', () => {
    expect(formatFileSize(512)).toBe('512 B')
  })

  it('formats kilobytes', () => {
    expect(formatFileSize(1024)).toBe('1.0 KB')
  })

  it('formats megabytes', () => {
    expect(formatFileSize(1048576)).toBe('1.0 MB')
  })

  it('formats gigabytes', () => {
    expect(formatFileSize(1073741824)).toBe('1.0 GB')
  })
})

describe('formatDuration', () => {
  it('returns "-" for zero', () => {
    expect(formatDuration(0)).toBe('-')
  })

  it('returns "-" for negative', () => {
    expect(formatDuration(-5)).toBe('-')
  })

  it('formats seconds only', () => {
    expect(formatDuration(45)).toBe('0:45')
  })

  it('formats minutes and seconds', () => {
    expect(formatDuration(125)).toBe('2:05')
  })

  it('formats hours, minutes and seconds', () => {
    expect(formatDuration(3661)).toBe('1:01:01')
  })
})
