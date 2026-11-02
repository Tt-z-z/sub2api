<template>
  <BaseDialog
    :show="doc !== null"
    :title="t('modelPlaza.params.title', { model })"
    width="wide"
    close-on-click-outside
    @close="emit('close')"
  >
    <div v-if="doc" class="space-y-4 text-sm">
      <p class="text-gray-600 dark:text-gray-300">{{ doc.summary }}</p>

      <div class="flex flex-wrap gap-1.5">
        <span
          v-for="endpoint in doc.endpoints"
          :key="endpoint"
          class="rounded-md bg-gray-100 px-2 py-0.5 font-mono text-xs text-gray-700 dark:bg-dark-700 dark:text-gray-200"
          >{{ endpoint }}</span
        >
        <span
          v-for="spec in doc.specs"
          :key="spec"
          class="rounded-md bg-primary-50 px-2 py-0.5 text-xs text-primary-700 dark:bg-primary-900/20 dark:text-primary-300"
          >{{ spec }}</span
        >
      </div>

      <div class="overflow-x-auto rounded-lg border border-gray-100 dark:border-dark-700">
        <table class="w-full min-w-[640px] border-collapse text-left text-xs">
          <thead class="bg-gray-50 text-gray-500 dark:bg-dark-800 dark:text-dark-400">
            <tr>
              <th class="w-[26%] px-3 py-2 font-medium">{{ t('modelPlaza.params.name') }}</th>
              <th class="w-[22%] px-3 py-2 font-medium">{{ t('modelPlaza.params.values') }}</th>
              <th class="w-[12%] px-3 py-2 font-medium">{{ t('modelPlaza.params.status') }}</th>
              <th class="px-3 py-2 font-medium">{{ t('modelPlaza.params.note') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="param in doc.params"
              :key="param.name"
              class="border-t border-gray-100 align-top dark:border-dark-700"
            >
              <td class="px-3 py-2 font-mono text-gray-900 dark:text-gray-100">{{ param.name }}</td>
              <td class="px-3 py-2 text-gray-600 dark:text-gray-300">{{ param.values || '-' }}</td>
              <td class="px-3 py-2">
                <span
                  :class="['whitespace-nowrap rounded-md px-1.5 py-0.5 text-[11px] font-medium', STATUS_CLASS[param.status]]"
                  >{{ t(`modelPlaza.params.statuses.${param.status}`) }}</span
                >
              </td>
              <td class="px-3 py-2 text-gray-600 dark:text-gray-300">{{ param.note }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <p class="text-xs text-gray-400 dark:text-dark-500">
        {{ t('modelPlaza.params.verifiedAt', { date: doc.verifiedAt }) }}
      </p>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { getModelParamDoc, type ModelParamStatus } from '@/utils/modelParamDetails'

const props = defineProps<{ model: string | null }>()
const emit = defineEmits<{ (e: 'close'): void }>()
const { t } = useI18n()

const doc = computed(() => (props.model ? getModelParamDoc(props.model) : null))

const STATUS_CLASS: Record<ModelParamStatus, string> = {
  effective: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-300',
  partial: 'bg-amber-50 text-amber-700 dark:bg-amber-900/20 dark:text-amber-300',
  ignored: 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-dark-300',
  rejected: 'bg-red-50 text-red-600 dark:bg-red-900/20 dark:text-red-300',
  unverified: 'bg-sky-50 text-sky-700 dark:bg-sky-900/20 dark:text-sky-300'
}
</script>
