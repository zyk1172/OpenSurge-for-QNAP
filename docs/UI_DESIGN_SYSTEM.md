# OpenSurge Web UI 统一设计系统

本文是 OpenSurge Web UI 的视觉与交互契约。新增页面和组件不得再通过借用其他业务页面的 class 获得视觉样式。

## 参考来源与取舍

设计架构参考 MoviePilot Frontend V3 当前实现，重点吸收以下稳定做法：

- 在全局层定义 surface、圆角、阴影、控件和 motion token；
- 卡片、Sheet、弹窗等主要 surface 共用同一套契约；
- 默认卡片不带 elevation，只有明确可交互的卡片才 opt-in 上浮反馈；
- 主题只替换材质 token，不改变业务组件的尺寸和结构契约；
- 页面、Dialog、Popover、Bottom Sheet 使用统一的短时长动画和 easing；
- 输入、选择、按钮等控件由共享 defaults 管理，不允许页面自行创造新的控制高度、圆角或 focus ring。

不会照搬 MoviePilot 的以下实现：

- 页面内部的固定圆角、固定透明度、固定 blur、临时 `!important` 修补；
- Vuetify 依赖和 Vue 组件模型；
- 与 OpenSurge 信息密度、网络控制台使用场景无关的媒体卡片设计；
- 为追求玻璃效果而对每一层重复执行昂贵 backdrop blur。

OpenSurge 使用 React 原生组件和 CSS token 实现同一架构原则。

## 单一权威层

`web/src/design-system.css` 是共享视觉契约的唯一权威层，并在所有历史兼容 CSS 之后加载。

历史 CSS 在迁移期只允许保留：

- feature-specific layout；
- 旧页面结构兼容；
- 业务状态和响应式布局中尚未迁移的特殊规则。

不得再在历史文件中新增共享字体、卡片圆角、surface、按钮高度、focus、阴影或全局动画标准。新视觉规则必须进入 `design-system.css` 或共享 React primitive。

## Token 标准

### Typography

| Role | Size | Weight | 用途 |
| --- | ---: | ---: | --- |
| Page title | 26px | 650 | 页面 H1 |
| Section title | 16px | 650 | Panel 标题 |
| Card title | 14px | 650 | 卡片/状态标题 |
| Body | 13px | 400–550 | 正文 |
| Control | 13px | 600 | 按钮/输入控件 |
| Caption | 11px | 400–600 | 帮助、状态、表头 |
| Overline | 10px | 650 | 少量类别标识 |

字体族：`Inter, Noto Sans SC, system fallbacks`。

禁止在业务页面新增 7px、8px、9px 等不可读的普通文字。数据可视化内部若确有极小标签需求，应作为图表专用 token，而不是普通 UI 字号。

### Radius

| Role | Radius |
| --- | ---: |
| Surface / Panel / Card / Table | 12px |
| Form control / Button | 8px |
| Compact internal element | 6px |
| Pill / round indicator | 999px |

圆角差异必须来自组件语义，不允许来自 selector specificity 或页面历史。

### Spacing

统一步进：`4 / 8 / 12 / 16 / 20 / 24px`。

### Controls

桌面默认高度为 `38px`；窄屏/触控布局为 `44px`。按钮和输入共享控制圆角与 focus ring。

### Alignment

对齐由交互语义决定，不允许每个页面凭视觉感觉自行选择：

- 确认、警告、操作结果等短内容 Dialog：标题、摘要和操作按钮居中；
- Dialog 中的风险清单、技术细节、代码、地址和规则仍左对齐；
- 编辑器、配置预览、命令面板、连接证据检查器等工作型窗口整体左对齐；
- 空状态、单值状态和短提示可以居中；表格、表单标签和可扫描的长文本默认左对齐。

禁止用全局 `dialog { text-align:center }` 之类规则覆盖工作型窗口。

### Application chrome stability

侧边栏展开/收起属于同一个控件状态转换。折叠按钮在两种状态下必须保持相同的垂直基线，只允许图标方向和侧栏宽度变化，不允许通过 `position:absolute`、`top` 或额外上边距把按钮移动到另一行。折叠按钮是次要控制，尺寸应小于普通 38px 操作按钮，但仍保留清晰的 focus ring 和点击反馈。

### Motion

- Page enter：180ms；
- Overlay/Dialog：160ms；
- 普通 control feedback：160ms；
- Standard easing：`cubic-bezier(.2,.8,.2,1)`；
- Exit easing：`cubic-bezier(.4,0,1,1)`。

遵守 `prefers-reduced-motion`。

## Surface 层级

### Page Header

页面入口表面，承担 H1、说明和主要动作。使用基础 surface，不额外发光或渐变。

### Panel

页面的主要结构分组。React primitive：`Panel`，CSS primitive：`.ui-panel`。

### Card

Panel 内部的次级对象。使用 `.ui-card`。默认是静态表面：**不移动、不自动增加阴影**。

### Interactive Card

只有整卡本身可以点击、导航或选择时，才增加 `.ui-interactive`。Hover 上浮约 4px，并提升 border/shadow。

表单卡、设置卡、状态卡不得因为名字中包含 `card` 就自动上浮。

### Raised / Overlay

Dialog、Command Palette、sticky action bar 等浮层使用 raised surface 和 stronger shadow。业务卡片不得模仿 overlay elevation。

## Shared React primitives

`web/src/components/Common.tsx` 提供：

- `PageHeader`
- `Panel`
- `SectionHeader`
- `SectionTitle`（兼容旧页面，视觉契约映射到 `SectionHeader`）
- `FormField`
- `TableSurface`
- `ActionBar`
- `Empty`
- `Metric`
- `Service`
- `StatusDot`

新增页面优先组合这些 primitive，而不是复制 DOM + class。

## Feature CSS 边界

业务页面允许定义：

- grid 列数；
- feature-specific responsive layout；
- 特定图表几何；
- 特殊信息结构。

业务页面禁止自行定义：

- 基础 surface 背景；
- 通用卡片圆角；
- 通用按钮高度；
- 通用输入框边框/focus；
- 页面/Section 字体等级；
- 通用 shadow；
- 通用 hover lift；
- 独立的全局 glass blur 标准。

例如 Cloudflare 页面可以定义 `cloudflare-target-fields` 的列布局，但不能借用 `source-card`，也不能自行给目标卡设置一套背景和圆角。

## Cross-feature class 禁止规则

以下模式禁止：

```tsx
<div className="source-card cloudflare-target-card" />
<section className="connectivity-overview">...</section>
```

如果一个视觉结构被两个业务功能共享，应提升为共享 primitive，而不是借用另一个业务域的 class。

## Cloudflare 作为迁移样板

Cloudflare Optimizer 是新设计系统的第一张完整样板页面：

- summary 使用 `.ui-summary-grid`；
-主要分区使用 `Panel`；
- 标题使用 `SectionHeader`；
- 表单使用 `FormField` + `.ui-form-grid`；
- 目标编辑器使用静态 `.ui-card`；
- 结果使用 `TableSurface` + `.ui-table`；
- 保存区使用 `ActionBar`；
- 所有用户可见文本走 `t()`；
- 日期时间使用应用 locale，而不是浏览器默认 locale。

其他页面迁移时以这些组件契约为准，不复制 Cloudflare 的 feature layout class。

## QA 门槛

每次修改共享视觉层至少验证：

1. QNAP dark / light；
2. desktop / mobile；
3. PageHeader、Panel、Card、表单、Table、ActionBar；
4. hover / focus / disabled / reduced-motion；
5. 中英文；
6. 页面不得重新出现跨业务视觉 class 依赖；
7. computed style 中共享 surface/radius/type/control 必须来自统一 token；
8. 确认型 Dialog 与工作型 Dialog 的文字对齐符合 Alignment 规则；
9. 侧栏展开/收起前后折叠按钮保持同一垂直基线；
10. 普通业务正文不得回退到 7–9px 微字号，Caption 下限按 11px 标准执行。

在浏览器视觉回归基础设施加入前，CI 至少通过 React/Vitest、TypeScript 和 QNAP production build；视觉回归应作为后续门槛加入。
