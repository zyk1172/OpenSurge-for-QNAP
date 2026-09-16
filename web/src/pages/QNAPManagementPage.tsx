import { PageHeader } from '../components/Common'
import { RemoteManagementCard } from '../components/RemoteManagementCard'

export function QNAPManagementPage() {
  return <>
    <PageHeader eyebrow="MANAGEMENT" title="管理" description="局域网 API 访问与管理凭据。" />
    <RemoteManagementCard />
  </>
}
