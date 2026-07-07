<!-- web/src/components/ui/HTable.vue -->
<script setup lang="ts" generic="T extends Record<string, any>">
interface HTableColumn {
  key: string
  label: string
}
defineProps<{
  columns: HTableColumn[]
  data: T[]
}>()
const emit = defineEmits<{ 'row-click': [row: T] }>()
</script>
<template>
  <div class="table-wrap">
    <table>
      <thead>
        <tr>
          <th v-for="col in columns" :key="col.key">{{ col.label }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="(row, i) in data" :key="i" @click="emit('row-click', row)">
          <td v-for="col in columns" :key="col.key">
            <slot v-if="$slots[`cell-${col.key}`]" :name="`cell-${col.key}`" :row="row" />
            <template v-else>{{ row[col.key] === null || row[col.key] === undefined ? '-' : row[col.key] }}</template>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
