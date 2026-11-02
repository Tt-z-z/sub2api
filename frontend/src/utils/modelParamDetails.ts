export type ModelParamStatus = 'effective' | 'partial' | 'ignored' | 'rejected' | 'unverified'

export interface ModelParamDetail {
  name: string
  values?: string
  status: ModelParamStatus
  note: string
}

export interface ModelParamDoc {
  summary: string
  endpoints: string[]
  specs: string[]
  params: ModelParamDetail[]
  verifiedAt: string
}

const VERIFIED_AT = '2026-11-02'

function textModelParams(efforts: string, effortNote: string, verbosityNote: string): ModelParamDetail[] {
  return [
    { name: 'reasoning.effort / reasoning_effort', values: efforts, status: 'effective', note: effortNote },
    { name: 'text.verbosity', values: 'low / medium / high', status: 'effective', note: verbosityNote },
    {
      name: 'text.format / response_format',
      values: 'json_schema、json_object、text',
      status: 'effective',
      note: 'json_schema（strict）在 /v1/responses 和 /v1/chat/completions 上均返回符合 schema 的 JSON。'
    },
    {
      name: 'tools / tool_choice / parallel_tool_calls',
      values: 'function、web_search 等',
      status: 'effective',
      note: '两个端点都能正常返回函数调用（实测 get_weather 返回 {"city":"Paris"}）。'
    },
    {
      name: 'input_image（图片输入）',
      values: 'URL 或 data URL',
      status: 'effective',
      note: '发送纯红色图片，模型正确回答 “Red”。'
    },
    { name: 'stream', values: 'true / false', status: 'effective', note: '流式返回 output_text.delta 与 response.completed 事件。' },
    {
      name: 'prompt_cache_key',
      values: '任意字符串',
      status: 'effective',
      note: '相同前缀第二次请求命中缓存：3319 个输入 token 中 3200 个按缓存读取计费。'
    },
    {
      name: 'reasoning.summary',
      values: 'auto / concise / detailed',
      status: 'partial',
      note: '仅流式请求返回推理摘要（实测 4 个摘要事件）；非流式响应不包含摘要。'
    },
    {
      name: 'service_tier',
      values: 'priority',
      status: 'ignored',
      note: '传 priority 不报错，但上游实际返回 default，当前账号不生效；传 flex 返回 400。'
    },
    {
      name: 'max_output_tokens / max_tokens / max_completion_tokens',
      values: '整数',
      status: 'ignored',
      note: '转发前会被移除，无法限制输出长度（限制 30，实际输出约 1700–2300 token）。'
    },
    {
      name: 'temperature / top_p / frequency_penalty / presence_penalty / logprobs',
      values: '数值',
      status: 'ignored',
      note: '请求可正常返回，但这些参数转发前会被移除，不影响输出。'
    },
    {
      name: 'stop',
      values: '字符串或数组',
      status: 'ignored',
      note: '设置 stop=["5"] 后输出仍为 “1 2 3 … 10”。'
    },
    {
      name: 'metadata / user / truncation / stream_options / prompt_cache_retention',
      status: 'ignored',
      note: '转发前会被移除。'
    }
  ]
}

function imageModelParams(): ModelParamDetail[] {
  return [
    { name: 'prompt', values: '文本', status: 'effective', note: '图片内容按提示词生成。' },
    {
      name: 'background',
      values: 'auto / opaque / transparent',
      status: 'effective',
      note: 'transparent 返回带透明通道的 PNG（color type 6），此时上游画质会提升为 medium。'
    },
    {
      name: 'images / mask（/v1/images/edits）',
      values: 'image_url（URL 或 data URL）',
      status: 'effective',
      note: '上传参考图后按提示词编辑并返回新图片。'
    },
    {
      name: 'response_format',
      values: 'b64_json / url',
      status: 'effective',
      note: 'url 会返回 data:image/png;base64 形式的地址。'
    },
    {
      name: 'stream',
      values: 'true / false',
      status: 'effective',
      note: '流式返回 image_generation.completed 事件。'
    },
    {
      name: 'size',
      values: 'auto、1024x1024、1536x1024、自定义 WxH',
      status: 'ignored',
      note: '当前上游账号固定输出约 150 万像素（如 1374x1145、1312x1199），请求 1536x864 或 1000x1000 都不改变尺寸。'
    },
    {
      name: 'quality',
      values: 'low / medium / high / xhigh / max / auto',
      status: 'ignored',
      note: '请求 max 时上游仍返回 quality=low。'
    },
    {
      name: 'output_format / output_compression',
      values: 'png / jpeg / webp，0–100',
      status: 'partial',
      note: '/v1/images 接口始终返回 PNG；改用 /v1/responses 的 image_generation 工具时 output_format=jpeg 可生效。'
    },
    { name: 'n', values: '整数', status: 'ignored', note: 'n=2 仍只返回 1 张图，按实际返回张数计费。' },
    { name: 'partial_images', values: '0–3', status: 'ignored', note: '流式请求未返回中间预览图。' },
    { name: 'moderation', values: 'auto / low', status: 'unverified', note: '请求正常返回，但无法观察到审核强度差异。' },
    { name: 'style', values: 'vivid / natural', status: 'ignored', note: '旧版图片模型参数，GPT Image 不支持，请在提示词中描述风格。' }
  ]
}

const IMAGE_SUMMARY =
  '当前上游为 ChatGPT Plus 账号，尺寸和画质由上游固定，以下为通过本站实际请求测得的结果。'

export const MODEL_PARAM_DOCS: Record<string, ModelParamDoc> = {
  'gpt-6.1-sol': {
    summary: '主力编程与日常模型。推理强度、输出详细度、结构化输出、工具调用和缓存均可用；长度与采样类参数不生效。',
    endpoints: ['POST /v1/responses', 'POST /v1/chat/completions'],
    specs: ['上下文 272K（最大 872K）', '输入：文本、图片', '默认推理强度 low'],
    params: textModelParams(
      'low / medium / high / xhigh / max（none、minimal 会按 none 处理）',
      '实测推理 token：low 173 → high 227 → max 370，强度越高思考越久；不同强度按定价表中的倍率计费。',
      '同一问题 low 输出 590 字符，high 输出 1678 字符。'
    ),
    verifiedAt: VERIFIED_AT
  },
  'gpt-6.1-terra': {
    summary: '快速高效的视觉推理与写作模型。推理强度、输出详细度、结构化输出、工具调用和缓存均可用；长度与采样类参数不生效。',
    endpoints: ['POST /v1/responses', 'POST /v1/chat/completions'],
    specs: ['官方上下文 1,050,000', '官方最大输出 128,000', '输入：文本、图片'],
    params: textModelParams(
      'none / low / medium / high / xhigh / max（minimal 会按 none 处理）',
      '实测推理 token：low 161 → high 215 → max 204，模型会按题目难度自适应；不同强度按定价表中的倍率计费。',
      '同一问题 low 输出 510 字符，high 输出 1518 字符。'
    ),
    verifiedAt: VERIFIED_AT
  },
  'gpt-image-2.5-flare': {
    summary: `速度优先的日常生图模型。${IMAGE_SUMMARY}`,
    endpoints: ['POST /v1/images/generations', 'POST /v1/images/edits'],
    specs: ['输入：文本、图片', '输出：图片', '按张计费'],
    params: imageModelParams(),
    verifiedAt: VERIFIED_AT
  },
  'gpt-image-2.5-sunburst': {
    summary: `画质优先、擅长精细编辑的生图模型。${IMAGE_SUMMARY}`,
    endpoints: ['POST /v1/images/generations', 'POST /v1/images/edits'],
    specs: ['输入：文本、图片', '输出：图片', '按张计费'],
    params: imageModelParams(),
    verifiedAt: VERIFIED_AT
  }
}

export function getModelParamDoc(model: string): ModelParamDoc | null {
  return MODEL_PARAM_DOCS[model.trim().toLowerCase()] ?? null
}
