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
}

registerEnglishMessages(shellEnglishMessages)
