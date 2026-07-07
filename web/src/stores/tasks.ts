import { defineStore } from 'pinia'
import { ref } from 'vue'
import type { Task, TaskProgressEvent } from '@/api/types-derived'
import { listTasks } from '@/api/tasks'

export const useTasksStore = defineStore('tasks', () => {
  const items = ref<Task[]>([])
  const loading = ref(false)

  async function fetchTasks(): Promise<void> {
    loading.value = true
    try {
      const response = await listTasks()
      items.value = response.items
    } finally {
      loading.value = false
    }
  }

  function handleTaskProgress(event: TaskProgressEvent): void {
    const index = items.value.findIndex((t) => t.id === event.task_id)
    if (index !== -1) {
      const task = items.value[index]
      task.status = event.status
      task.progress = event.progress
      task.message = event.message
      task.error = event.error || task.error
      if (event.status === 'succeeded' || event.status === 'failed' || event.status === 'cancelled') {
        task.finished_at = new Date().toISOString()
      }
      items.value.splice(index, 1, { ...task })
    } else {
      // Unknown task - full refresh to get complete data
      fetchTasks()
    }
  }

  return { items, loading, fetchTasks, handleTaskProgress }
})
