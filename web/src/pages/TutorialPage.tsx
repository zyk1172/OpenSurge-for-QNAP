import { PageHeader, SectionTitle } from '../components/Common'
import { t } from '../i18n'

export function TutorialPage() {
  return <>
    <PageHeader eyebrow="GUIDE" title={t('教程')} description={t('集中说明 QNAP 网络边界、高级代理配置与部署网络修改。')} />

    <section className="section">
      <SectionTitle title={t('QNAP 网络边界')} subtitle={t('先确认哪些流量由 OpenSurge 接管')} />
      <div className="tutorial-grid">
        <article className="tutorial-card">
          <h3>{t('IPv6 不由 NAS 主机接管')}</h3>
          <p>{t('当前 QNAP NAS 主机接管仅处理宿主机公网 IPv4。IPv6 继续由 QTS 与现有网络管理，不会被 OpenSurge 主机接管策略改写。')}</p>
        </article>
        <article className="tutorial-card">
          <h3>{t('Tailscale 共存')}</h3>
          <p>{t('启用共存保护时，Tailscale 接口、MagicDNS 与 Tailnet 路由保持更高优先级；普通公网 IPv4 仍可经过 OpenSurge。')}</p>
        </article>
      </div>
    </section>

    <section className="section">
      <SectionTitle title={t('高级代理配置')} subtitle={t('在“代理与规则源”中统一维护')} />
      <div className="tutorial-grid">
        <article className="tutorial-card">
          <h3>{t('Profile Overlay')}</h3>
          <p>{t('规则、Provider、策略组、Hosts 与 DNS 高级项通过全局附加配置维护。它与导入来源组合后生成最终 Mihomo 配置，并在应用前执行校验。')}</p>
        </article>
        <article className="tutorial-card">
          <h3>{t('配置来源与草稿')}</h3>
          <p>{t('HTTPS 订阅和本地 YAML 导入后先保存为草稿。选择应用后，OpenSurge 才会把来源与全局附加配置组合为运行配置。')}</p>
        </article>
      </div>
    </section>

    <section className="section">
      <SectionTitle title={t('部署网络修改')} subtitle={t('QNET 参数属于容器创建边界')} />
      <div className="tutorial-grid">
        <article className="tutorial-card">
          <h3>{t('需要重建容器的项目')}</h3>
          <p>{t('QNET 父接口、OpenSurge 静态 IPv4、LAN CIDR 或上游主路由需要在 Compose / Container Station 中修改，运行中的 Web 不直接修改这些 QTS 网络参数。')}</p>
        </article>
        <article className="tutorial-card">
          <h3>{t('保留现有配置')}</h3>
          <p>{t('重建容器时继续挂载原来的 /data 持久化目录，即可保留 OpenSurge 配置、来源、认证与运行状态记录。')}</p>
        </article>
      </div>
    </section>
  </>
}
