import { registerEnglishMessages } from './i18n'

export const shellEnglishMessages: Record<string, string> = {
  '搜索 OpenSurge 页面…': 'Search OpenSurge pages…',
  '搜索 OpenSurge 页面': 'Search OpenSurge pages',
  '快速跳转': 'Quick navigation',
  '输入页面名称，然后回车打开。': 'Type a page name, then press Enter to open it.',
  'OpenSurge 页面': 'OpenSurge pages',
  '当前页面': 'Current page',
  '没有匹配的页面': 'No matching pages',
  '搜索页面、功能或状态…': 'Search pages, features, or status…',
  '打开快速跳转': 'Open quick navigation',
  '重启网关': 'Restart gateway',
  '正在重启…': 'Restarting…',
  '网关重启失败': 'Gateway restart failed',
  'Cloudflare 优选': 'Cloudflare Optimizer',
  '正在加载优选配置…': 'Loading optimizer settings…',
  '为指定域名筛选更合适的 Cloudflare IPv4，并写入最终 Mihomo Hosts 与真实 IP 规则。测速强制绑定 QNAP 容器物理接口，整轮受时间预算限制。': 'Find better Cloudflare IPv4 addresses for selected hostnames and inject them into the final Mihomo Hosts and real-IP rules. Probes are bound to the QNAP container physical interface and constrained by a total time budget.',
  '正在优选…': 'Optimizing…',
  '立即优选': 'Optimize now',
  '正在执行优选': 'Optimization in progress',
  '优选结果已应用': 'Optimized results applied',
  '等待首次优选': 'Waiting for the first optimization',
  '配置已保存': 'Configuration saved',
  '保存设置': 'Save settings',
}

registerEnglishMessages(shellEnglishMessages)
