<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { apiClient } from '@/api/client'
import AppLayout from '@/components/layout/AppLayout.vue'

type Entry = { id: string; key_id: number; method: string; route: string; bytes: number; state: string; deadline: number; preview?: string; raw?: string; text?: string }
const items = ref<Entry[]>([])
const drafts = ref<Record<string, string>>({})
const online = ref(false)
const busy = ref(false)
const now = ref(Date.now())
const keyID = ref(0)
const keyInput = ref('')
const keyMessage = ref('')
let disposed = false
let polling = false
let offset = 0
let pollTimer: ReturnType<typeof setInterval> | undefined
let clockTimer: ReturnType<typeof setInterval> | undefined
const controller = new AbortController()

async function refresh() {
  if (polling || disposed) return
  polling = true
  try {
    const { data } = await apiClient.get('/admin/request-approvals', { signal: controller.signal })
    if (disposed) return
    items.value = data.items
    const live = new Set<string>((data.items || []).map((item: Entry) => item.id))
    for (const id of Object.keys(drafts.value)) {
      if (!live.has(id)) delete drafts.value[id]
    }
    for (const item of data.items || []) {
      if (drafts.value[item.id] === undefined) drafts.value[item.id] = userText(item)
    }
    keyID.value = data.key_id
    offset = data.server_time - Date.now()
    now.value = Date.now() + offset
    online.value = true
  } catch { if (!disposed) online.value = false }
  finally { polling = false }
}
async function saveKey() {
  if (!online.value || busy.value) return
  busy.value = true
  keyMessage.value = ''
  try {
    const { data } = await apiClient.put('/admin/request-approvals/target', { key: keyInput.value }, { signal: controller.signal })
    keyInput.value = ''
    keyID.value = data.key_id
    keyMessage.value = data.key_id ? `已保护 Key ID ${data.key_id}` : '已取消保护'
    await refresh()
  } catch {
    keyMessage.value = '密钥无效或未启用'
  } finally { busy.value = false }
}
function userText(item: Entry) {
  if (typeof item.text === 'string') return item.text
  if (!item.preview || item.preview.includes('…用户输入过长')) return ''
  const marker = '用户输入\n'
  const index = item.preview.lastIndexOf(marker)
  return index >= 0 ? item.preview.slice(index + marker.length) : ''
}
async function decide(item: Entry, action: 'approve' | 'reject') {
  if (!online.value || busy.value) return
  busy.value = true
  try {
    const payload = action === 'approve' ? { text: drafts.value[item.id] ?? userText(item) } : {}
    await apiClient.post(`/admin/request-approvals/${encodeURIComponent(item.id)}/${action}`, payload, { signal: controller.signal })
    await refresh()
  } catch { online.value = false }
  finally { busy.value = false }
}
function remaining(deadline: number) {
  return Math.max(0, Math.ceil((deadline - now.value) / 1000))
}
onMounted(() => {
  void refresh()
  pollTimer = setInterval(() => void refresh(), 2000)
  clockTimer = setInterval(() => { now.value = Date.now() + offset }, 250)
})
onUnmounted(() => {
  disposed = true
  controller.abort()
  clearInterval(pollTimer)
  clearInterval(clockTimer)
})
</script>

<template>
  <AppLayout>
    <div class="space-y-6">
      <section class="card p-5">
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div>
            <h1 class="text-xl font-semibold text-gray-900 dark:text-white">请求审批</h1>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">仅测试环境生效。保存密钥后，该密钥的每条请求最多等待 180 秒，批准后才会继续转发。</p>
          </div>
          <span :class="online ? 'badge badge-success' : 'badge badge-danger'">{{ online ? '已连接' : '未连接' }}</span>
        </div>
        <form class="mt-5 grid gap-3 md:grid-cols-[1fr_auto]" @submit.prevent="saveKey">
          <input v-model="keyInput" type="password" autocomplete="off" class="input" placeholder="粘贴要测试的完整密钥，留空保存则取消保护">
          <button class="btn btn-primary" :disabled="!online || busy">保存保护密钥</button>
        </form>
        <p class="mt-3 text-sm text-gray-600 dark:text-gray-300">当前保护 Key ID：{{ keyID || '未设置' }}</p>
        <p v-if="keyMessage" class="mt-1 text-sm text-gray-600 dark:text-gray-300">{{ keyMessage }}</p>
      </section>

      <p v-if="!online" role="alert" class="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900 dark:bg-red-950 dark:text-red-300">审批台未连接或操作冲突，操作已禁用。正在自动重试，没有自动放行。</p>

      <section class="card overflow-hidden">
        <div class="border-b border-gray-200 px-5 py-4 dark:border-dark-700">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">等待审批</h2>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">用户输入可以直接修改。批准时会替换转发给模型的这一段，系统提示和其他轮次保持原样。拒绝、超时或未修改则按原请求处理。这段内容不会写入审计。</p>
        </div>
        <p v-if="online && items.length === 0" class="px-5 py-10 text-center text-sm text-gray-500">暂无等待审批的请求。</p>
        <div v-for="item in items" :key="item.id" class="border-b border-gray-100 px-5 py-4 last:border-b-0 dark:border-dark-800">
          <div class="flex flex-wrap items-center justify-between gap-4">
            <div class="min-w-0">
              <div class="font-mono text-sm text-gray-900 dark:text-white">{{ item.method }} {{ item.route }}</div>
              <div class="mt-1 text-sm text-gray-500 dark:text-gray-400">Key {{ item.key_id }} · {{ item.bytes }} 字节 · 剩余 {{ remaining(item.deadline) }} 秒</div>
              <div class="mt-1 truncate font-mono text-xs text-gray-400">{{ item.id }}</div>
            </div>
            <div class="flex gap-2">
              <button class="btn btn-primary" :disabled="!online || busy || item.state !== 'pending' || item.deadline <= now" @click="decide(item, 'approve')">批准本条</button>
              <button class="btn btn-secondary" :disabled="!online || busy || item.state !== 'pending' || item.deadline <= now" @click="decide(item, 'reject')">拒绝本条</button>
            </div>
          </div>
          <label v-if="userText(item)" class="mt-3 block text-sm text-gray-500 dark:text-gray-400">
            用户输入
            <textarea v-model="drafts[item.id]" class="input mt-1 min-h-28 w-full whitespace-pre-wrap font-sans text-sm" :disabled="!online || busy || item.state !== 'pending' || item.deadline <= now" />
          </label>
          <pre v-else-if="item.preview" class="mt-3 max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-3 font-sans text-sm text-gray-800 dark:bg-dark-800 dark:text-gray-100">{{ item.preview }}</pre>
          <details v-if="item.raw" class="mt-3">
            <summary class="cursor-pointer text-sm text-gray-500 dark:text-gray-400">查看原请求</summary>
            <pre class="mt-2 max-h-96 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-3 font-mono text-xs text-gray-800 dark:bg-dark-800 dark:text-gray-100">{{ item.raw }}</pre>
          </details>
        </div>
      </section>
    </div>
  </AppLayout>
</template>
