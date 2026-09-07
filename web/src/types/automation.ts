export type AutomationTask = {
  id: string
  name: string
  prompt: string
  projectId: string
  workspace: string
  profileId?: string
  triggerType: 'manual' | 'interval'
  intervalMinutes?: number
  repeat?: 'interval' | 'daily' | 'weekdays' | 'weekly'
  scheduleTime?: string
  scheduleWeekday?: number
  enabled: boolean
  nextRunAt?: string
  lastRunAt?: string
  lastStatus?: string
  lastError?: string
  lastSessionId?: string
  createdAt: string
  updatedAt: string
}
