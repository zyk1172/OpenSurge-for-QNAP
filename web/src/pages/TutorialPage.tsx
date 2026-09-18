import { useState } from 'react'
import { PageHeader, Panel, SectionHeader } from '../components/Common'
import { t } from '../i18n'
import './TutorialPage.css'

export function TutorialPage() {
  const [copyState, setCopyState] = useState<'idle' | 'copied' | 'error'>('idle')

  const copyAIInstructions = async () => {
    const value = [
      `OpenSurge · ${t('AI 分流分析')}`,
      `1. GET /api/remote/v1/diagnostics — ${t('读取当前连接、命中规则、实际出口链和脱敏日志')}`,
      `2. GET /api/remote/v1/policies — ${t('解释策略组当前选择')}`,
      `3. GET / PUT /api/remote/v1/profile-overlay — ${t('读取或写入 rules.prepend 高优先级规则')}`,
      `4. ${t('正常 DIRECT、DNS、LAN 和策略组换节点不是异常；只基于可验证证据提出修正。')}`,
      `5. ${t('规则确认后再写入覆盖层，并重新应用当前来源。')}`,
    ].join('\n')
    try {
      await copyText(value)
      setCopyState('copied')
    } catch {
      setCopyState('error')
    }
  }

  return <>
    <PageHeader eyebrow="GUIDE" title={t('教程')} description={t('从首次部署到日常维护：网络、来源、策略、设备、Hosts、诊断与安全边界。')} />

    <Panel>
      <SectionHeader title="QNAP 网络边界" subtitle="先确认哪些流量由 OpenSurge 接管" />
      <div className="tutorial-grid">
        <article className="ui-card tutorial-card">
          <h3>{t('IPv6 不由 NAS 主机接管')}</h3>
          <p>{t('当前 QNAP NAS 主机接管仅处理宿主机公网 IPv4。IPv6 继续由 QTS 与现有网络管理，不会被 OpenSurge 主机接管策略改写。')}</p>
        </article>
        <article className="ui-card tutorial-card">
          <h3>{t('Tailscale 共存')}</h3>
          <p>{t('启用共存保护时，Tailscale 接口、MagicDNS 与 Tailnet 路由保持更高优先级；普通公网 IPv4 仍可经过 OpenSurge。')}</p>
        </article>
      </div>
    </Panel>

    <Panel>
      <SectionHeader title="高级代理配置" subtitle="在“代理与规则源”中统一维护" />
      <div className="tutorial-grid">
        <article className="ui-card tutorial-card">
          <h3>{t('Profile Overlay')}</h3>
          <p>{t('规则、Provider、策略组、Hosts 与 DNS 高级项通过全局附加配置维护。它与导入来源组合后生成最终 Mihomo 配置，并在应用前执行校验。')}</p>
        </article>
        <article className="ui-card tutorial-card">
          <h3>{t('配置来源与草稿')}</h3>
          <p>{t('HTTPS 订阅和本地 YAML 导入后先保存为草稿。选择应用后，OpenSurge 才会把来源与全局附加配置组合为运行配置。')}</p>
        </article>
      </div>
    </Panel>

    <Panel><SectionHeader title="从部署到可用" subtitle="推荐按顺序完成，减少网络中断和配置漂移" /><div className="tutorial-grid">
      <article className="ui-card tutorial-card"><h3>{t('1 · 创建容器')}</h3><p>{t('选择正确的 QNET 父接口，设置固定 OpenSurge IPv4、LAN CIDR、主路由和持久化 /data。需要同步 NAS Hosts 时保留 /etc/hosts 只读映射。')}</p></article>
      <article className="ui-card tutorial-card"><h3>{t('2 · 网络设置')}</h3><p>{t('确认容器接口、OpenSurge IPv4、LAN 与主路由。NAS 主机接管是可选功能，应在网关稳定后启用。')}</p></article>
      <article className="ui-card tutorial-card"><h3>{t('3 · 来源与策略')}</h3><p>{t('导入 HTTPS 订阅或 YAML 后先形成快照；应用后再到策略页选择出口和测速。')}</p></article>
      <article className="ui-card tutorial-card"><h3>{t('4 · 设备与分流')}</h3><p>{t('登记设备 ID、IPv4、MAC，选择跟随网关、独立出口或主路由，再按需要添加规则集和设备分流。')}</p></article>
      <article className="ui-card tutorial-card"><h3>{t('5 · Hosts')}</h3><p>{t('手工 Hosts、原生 Mihomo Hosts 和 NAS 宿主 Hosts 可以并存。宿主同步使用独立托管区，不覆盖手工内容。QNAP 宿主 /etc/hosts 必须只读映射到 /run/opensurge/host-hosts；旧容器升级后需要补充挂载并重建容器。')}</p></article>
      <article className="ui-card tutorial-card"><h3>{t('6 · 验证与诊断')}</h3><p>{t('先用连通性检查路径，再到诊断查看代理集合、连接、操作记录和近期日志。')}</p></article></div></Panel>
    <Panel><SectionHeader title="使用与维护注意事项" subtitle="区分即时生效、重载和重建容器" /><div className="tutorial-grid">
      <article className="ui-card tutorial-card"><h3>{t('即时操作')}</h3><p>{t('策略节点切换、测速、连通性检测和手动 Hosts 同步可以直接执行。')}</p></article>
      <article className="ui-card tutorial-card"><h3>{t('保存与重载')}</h3><p>{t('设备身份、路由方式、规则和附加配置改变后，以 desired/applied 状态为准；已保存不等于已运行。')}</p></article>
      <article className="ui-card tutorial-card"><h3>{t('重建容器')}</h3><p>{t('QNET 父接口、容器静态 IPv4、LAN CIDR、主路由和新增宿主文件映射属于创建边界。')}</p></article>
      <article className="ui-card tutorial-card"><h3>{t('排障顺序')}</h3><p>{t('总览 → 网络 → 来源 desired/applied → 策略节点 → 连通性 → 诊断日志。避免同时盲改 DNS、路由和规则。')}</p></article></div></Panel>
    <Panel>
      <SectionHeader title="AI 分流分析" subtitle="AI 读取相同的连接证据、策略组和覆盖层；正常路径不应被当成异常。" />
      <div className="tutorial-ai-flow">
        <article className="ui-card tutorial-ai-step"><strong>1</strong><div><code>GET /api/remote/v1/diagnostics</code><p>{t('读取当前连接、命中规则、实际出口链和脱敏日志')}</p></div></article>
        <article className="ui-card tutorial-ai-step"><strong>2</strong><div><code>GET /api/remote/v1/policies</code><p>{t('解释策略组当前选择')}</p></div></article>
        <article className="ui-card tutorial-ai-step"><strong>3</strong><div><code>GET / PUT /api/remote/v1/profile-overlay</code><p>{t('读取或写入 rules.prepend 高优先级规则')}</p></div></article>
      </div>
      <div className="tutorial-ai-note">
        <p>{t('正常 DIRECT、DNS、LAN 和策略组换节点不是异常；只基于可验证证据提出修正。')}</p>
        <p>{t('规则确认后再写入覆盖层，并重新应用当前来源。')}</p>
      </div>
      <div className="tutorial-actions">
        <button className="ui-button ui-button--primary" type="button" onClick={() => void copyAIInstructions()}>{t('复制 AI 分流分析说明')}</button>
      </div>
      {copyState === 'copied' && <div className="ok-notice" role="status"><p>{t('AI 分流分析说明已复制。')}</p></div>}
      {copyState === 'error' && <div className="notice warn" role="alert"><p>{t('复制失败，请检查浏览器剪贴板权限。')}</p></div>}
    </Panel>

    <Panel>
      <SectionHeader title="部署网络修改" subtitle="QNET 参数属于容器创建边界" />
      <div className="tutorial-grid">
        <article className="ui-card tutorial-card">
          <h3>{t('需要重建容器的项目')}</h3>
          <p>{t('QNET 父接口、OpenSurge 静态 IPv4、LAN CIDR 或上游主路由需要在 Compose / Container Station 中修改，运行中的 Web 不直接修改这些 QTS 网络参数。')}</p>
        </article>
        <article className="ui-card tutorial-card">
          <h3>{t('保留现有配置')}</h3>
          <p>{t('重建容器时继续挂载原来的 /data 持久化目录，即可保留 OpenSurge 配置、来源、认证与运行状态记录。')}</p>
        </article>
      </div>
    </Panel>
  </>
}

async function copyText(value: string) {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(value)
    return
  }
  const textarea = document.createElement('textarea')
  textarea.value = value
  textarea.style.position = 'fixed'
  textarea.style.opacity = '0'
  document.body.appendChild(textarea)
  textarea.select()
  document.execCommand('copy')
  textarea.remove()
}
