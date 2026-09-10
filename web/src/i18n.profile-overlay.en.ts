import { registerEnglishMessages } from './i18n'

export const profileOverlayEnglishMessages: Record<string, string> = {
  'Hosts 条目': 'Hosts entries',
  'Hosts 与本地解析': 'Hosts & local resolution',
  '导入标准 Hosts 文件生成 mihomo hosts 映射，并控制是否读取容器系统 Hosts。': 'Import a standard Hosts file into mihomo hosts mappings and control whether container system Hosts are also read.',
  '使用配置 Hosts': 'Use configured Hosts',
  '对应 mihomo dns.use-hosts；关闭后保留已导入内容，但 DNS 不使用这些映射。': 'Maps to mihomo dns.use-hosts. When disabled, imported entries are preserved but DNS does not use them.',
  '读取系统 Hosts': 'Read system Hosts',
  '对应 mihomo dns.use-system-hosts；QNAP Docker 中读取的是容器内 /etc/hosts。': 'Maps to mihomo dns.use-system-hosts. In QNAP Docker this reads /etc/hosts inside the container.',
  'Hosts 文件内容': 'Hosts file contents',
  '支持空行、# 注释、IPv4、IPv6 和一行多个主机名；重复的主机/IP 会自动去重，同一主机的多个 IP 会合并。': 'Supports blank lines, # comments, IPv4, IPv6, and multiple hostnames per line. Duplicate host/IP pairs are deduplicated and multiple IPs for one host are combined.',
  '导入本地 Hosts 文件': 'Import local Hosts file',
}

registerEnglishMessages(profileOverlayEnglishMessages)
