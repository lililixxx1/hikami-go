<!--
  MCPCardV10.vue。MCP 搜索工具配置卡(AI 联网搜索增强)。
  - 总开关 + max_tool_rounds(防死循环轮次)。
  - 内置搜索 API key(Brave/Tavily,密钥只写,显示已设置/未设置)。
  - 外部 MCP server 列表(http/sse/stdio,可增删改)。
  保存后后端热重载连接(无需重启)。未配置或失败时降级普通 AI 调用(零回归)。
  L3 视觉验证,无单测(参照 ToolsCardV10)。
-->
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { HMessage } from '@/components/ui/message'
import { HCard, HButton, HInput, HSelect, HSwitch, HTextarea } from '@/components/ui'
import { getMCPConfig, updateMCPConfig } from '@/api/settings'
import type { MCPConfig, MCPServerConfig, MCPConfigUpdate } from '@/api/settings'

const emit = defineEmits<{ saved: [] }>()

const enabled = ref(false)
const maxToolRounds = ref('5')
const servers = ref<MCPServerConfig[]>([])
// 密钥字段(只写:本地草稿,GET 不返回明文)
const braveKey = ref('')
const braveKeyEnv = ref('BRAVE_API_KEY')
const braveKeySet = ref(false)
const tavilyKey = ref('')
const tavilyKeyEnv = ref('TAVILY_API_KEY')
const tavilyKeySet = ref(false)
const saving = ref(false)

const transportOptions = [
  { label: 'HTTP (Streamable)', value: 'http' },
  { label: 'SSE', value: 'sse' },
  { label: 'Stdio (子进程)', value: 'stdio' },
]

// headers(map) ↔ 多行文本(每行 KEY: VALUE)的双向转换,供 HTextarea 使用。
function headersToText(headers?: Record<string, string>): string {
  if (!headers) return ''
  return Object.entries(headers).map(([k, v]) => `${k}: ${v}`).join('\n')
}
function textToHeaders(text: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const line of text.split('\n')) {
    const t = line.trim()
    if (!t) continue
    const sep = t.indexOf(':')
    if (sep <= 0) continue // 跳过空 key / 无冒号行
    out[t.slice(0, sep).trim()] = t.slice(sep + 1).trim()
  }
  return out
}

async function fetchConfig() {
  try {
    const cfg: MCPConfig = await getMCPConfig()
    enabled.value = cfg.enabled
    maxToolRounds.value = String(cfg.max_tool_rounds || 5)
    servers.value = cfg.servers || []
    braveKeySet.value = cfg.builtin?.brave_api_key_set ?? false
    braveKeyEnv.value = cfg.builtin?.brave_api_key_env || 'BRAVE_API_KEY'
    tavilyKeySet.value = cfg.builtin?.tavily_api_key_set ?? false
    tavilyKeyEnv.value = cfg.builtin?.tavily_api_key_env || 'TAVILY_API_KEY'
    // 密钥草稿清空(只写,不回显)
    braveKey.value = ''
    tavilyKey.value = ''
  } catch { /* error shown by interceptor */ }
}

function addServer() {
  servers.value.push({
    name: '',
    transport: 'http',
    url: '',
    command: '',
    args: [],
    env: [],
    enabled: true,
    timeout_sec: 30,
    headers: {},
  })
}

function removeServer(idx: number) {
  servers.value.splice(idx, 1)
}

async function save() {
  saving.value = true
  try {
    const update: MCPConfigUpdate = {
      enabled: enabled.value,
      max_tool_rounds: Number(maxToolRounds.value) || 5,
      servers: servers.value,
      builtin: {
        brave_api_key_env: braveKeyEnv.value,
        tavily_api_key_env: tavilyKeyEnv.value,
      },
    }
    // 密钥仅在用户填了值时提交(空串=清空,这里只有用户主动输入才发送)
    if (braveKey.value) {
      update.builtin!.brave_api_key = braveKey.value
    }
    if (tavilyKey.value) {
      update.builtin!.tavily_api_key = tavilyKey.value
    }
    await updateMCPConfig(update)
    HMessage.success('MCP 配置已保存,已热重载连接')
    await fetchConfig() // 刷新密钥状态
    emit('saved')
  } catch { /* error shown by interceptor */ }
  finally {
    saving.value = false
  }
}

onMounted(fetchConfig)
defineExpose({ reload: fetchConfig })
</script>

<template>
  <HCard>
    <template #header>
      <span class="card-title">AI 搜索(MCP)</span>
    </template>

    <div class="form-hint" style="margin-bottom: 12px;">
      启用后,生成回顾与发现术语时 AI 可主动联网搜索,核实人名/游戏名/专有词的标准写法(增强术语校正)。未配置或连接失败时自动降级为普通 AI 调用,不影响现有功能。
    </div>

    <div class="form-row-inline">
      <label class="form-label">启用 MCP</label>
      <div class="form-field">
        <HSwitch v-model="enabled" />
      </div>
    </div>

    <div class="form-row-inline">
      <label class="form-label">最大搜索轮次</label>
      <div class="form-field">
        <HInput v-model="maxToolRounds" placeholder="5" />
      </div>
    </div>

    <div class="section-divider">内置搜索工具</div>
    <div class="form-hint" style="margin-bottom: 10px;">
      配置搜索 API 密钥后启用内置搜索工具。两者皆空则无内置工具(仅外部 MCP server 可用)。密钥不回显,保存后显示是否已设置。
    </div>

    <div class="form-row-inline">
      <label class="form-label">Brave API Key</label>
      <div class="form-field">
        <HInput v-model="braveKey" :placeholder="braveKeySet ? '已设置(输入新值覆盖)' : '输入 Brave Search API Key'" type="password" />
      </div>
    </div>
    <div class="form-row-inline">
      <label class="form-label">Brave Key 环境变量</label>
      <div class="form-field">
        <HInput v-model="braveKeyEnv" placeholder="BRAVE_API_KEY" />
      </div>
    </div>

    <div class="form-row-inline">
      <label class="form-label">Tavily API Key</label>
      <div class="form-field">
        <HInput v-model="tavilyKey" :placeholder="tavilyKeySet ? '已设置(输入新值覆盖)' : '输入 Tavily API Key'" type="password" />
      </div>
    </div>
    <div class="form-row-inline">
      <label class="form-label">Tavily Key 环境变量</label>
      <div class="form-field">
        <HInput v-model="tavilyKeyEnv" placeholder="TAVILY_API_KEY" />
      </div>
    </div>

    <div class="section-divider">外部 MCP Server</div>
    <div class="form-hint" style="margin-bottom: 10px;">
      连接外部 MCP server(如自建搜索服务)。stdio 模式会派生子进程。连接失败不影响其他工具。
    </div>

    <div v-for="(srv, idx) in servers" :key="idx" class="server-block">
      <div class="form-row-inline">
        <label class="form-label">名称</label>
        <div class="form-field">
          <HInput v-model="srv.name" placeholder="如 my-search" />
        </div>
      </div>
      <div class="form-row-inline">
        <label class="form-label">传输方式</label>
        <div class="form-field">
          <HSelect v-model="srv.transport" :options="transportOptions" />
        </div>
      </div>
      <div v-if="srv.transport === 'http' || srv.transport === 'sse'" class="form-row-inline">
        <label class="form-label">URL</label>
        <div class="form-field">
          <HInput v-model="srv.url" placeholder="http://localhost:9090/mcp" />
        </div>
      </div>
      <div v-if="srv.transport === 'http' || srv.transport === 'sse'" class="form-row-inline">
        <label class="form-label">请求头</label>
        <div class="form-field">
          <HTextarea :model-value="headersToText(srv.headers)" @update:model-value="srv.headers = textToHeaders(String($event))" placeholder="每行一个,格式 KEY: VALUE(如 Authorization: Bearer xxx)" :rows="2" />
        </div>
      </div>
      <template v-else>
        <div class="form-row-inline">
          <label class="form-label">命令</label>
          <div class="form-field">
            <HInput v-model="srv.command" placeholder="如 npx 或 node" />
          </div>
        </div>
        <div class="form-row-inline">
          <label class="form-label">参数(逗号分隔)</label>
          <div class="form-field">
            <HInput :model-value="srv.args.join(', ')" @update:model-value="srv.args = String($event).split(',').map(s => s.trim()).filter(Boolean)" placeholder="-y, @mcp/server" />
          </div>
        </div>
      </template>
      <div class="form-row-inline">
        <label class="form-label">启用</label>
        <div class="form-field">
          <HSwitch v-model="srv.enabled" />
        </div>
        <HButton variant="danger" size="sm" @click="removeServer(idx)">删除</HButton>
      </div>
    </div>

    <div class="card-actions">
      <HButton variant="ghost" size="sm" @click="addServer">+ 添加 Server</HButton>
      <HButton variant="primary" :loading="saving" @click="save">保存配置</HButton>
    </div>
  </HCard>
</template>

<style scoped>
.section-divider {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-muted);
  margin: 20px 0 8px;
  padding-bottom: 6px;
  border-bottom: 1px solid var(--border);
}
.server-block {
  padding: 12px;
  margin-bottom: 12px;
  background: var(--bg-secondary, var(--bg));
  border: 1px solid var(--border);
  border-radius: 6px;
}
</style>
