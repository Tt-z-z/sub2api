import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import View from './RequestApprovalView.vue'
const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get, post, put: vi.fn() } }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<div><slot /></div>' } }))
describe('请求审批测试台', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    get.mockReset(); post.mockReset()
    get.mockResolvedValue({ data: { items: [{ id: 'one', key_id: 21, route: '/v1/responses', method: 'POST', bytes: 42, state: 'pending', deadline: Date.now() + 180000, preview: '用户输入\n你好', raw: '{"messages":[]}' }], key_id: 21, server_time: Date.now() } })
    post.mockResolvedValue({ data: { ok: true } })
  })
  afterEach(() => vi.useRealTimers())
  it('每两秒轮询、逐条审批并清理计时器', async () => {
    const wrapper = mount(View)
    await flushPromises()
    expect(wrapper.text()).toContain('180 秒')
    expect(wrapper.text()).toContain('查看原请求')
    const approve = wrapper.findAll('button').find(button => button.text() === '批准本条')
    expect(approve).toBeTruthy()
    const editor = wrapper.find('textarea')
    expect((editor.element as HTMLTextAreaElement).value).toBe('你好')
    await editor.setValue('改过的话')
    await approve!.trigger('click')
    await flushPromises()
    expect(post).toHaveBeenCalledWith('/admin/request-approvals/one/approve', { text: '改过的话' }, expect.anything())
    const count = get.mock.calls.length
    await vi.advanceTimersByTimeAsync(2000)
    expect(get.mock.calls.length).toBe(count + 1)
    wrapper.unmount()
    const stopped = get.mock.calls.length
    await vi.advanceTimersByTimeAsync(4000)
    expect(get.mock.calls.length).toBe(stopped)
  })
  it('断网与过期时禁用，支持单条拒绝', async () => {
    const wrapper = mount(View)
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === '拒绝本条')!.trigger('click')
    await flushPromises()
    expect(post.mock.calls[0]![0]).toContain('/reject')
    get.mockRejectedValue(new Error('offline'))
    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    expect(wrapper.find('button').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })
})
