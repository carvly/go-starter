# 架构图

事实来源：`docs/diagrams/`、`internal/archtest/`、`internal/app/`、`deploy/docker-compose.yml`、`internal/modules/*/postgres/tables.go`

改 `docs/diagrams/` 下的图，或改了图里画的东西（模块、模块间依赖、archtest 规则、进程拓扑、
外部依赖）时读这篇。图和 `docs/ARCHITECTURE.md` 一样描述**当前**代码，过时的图比没有图更糟。

## 三张图各回答什么

| 文件 | 回答的问题 | 事实来源 |
|---|---|---|
| `docs/diagrams/architecture-layers.html` | 哪个包能 import 哪个包，哪些被禁止 | `internal/archtest/` |
| `docs/diagrams/architecture-modules.html` | 模块之间怎么协作、各模块有哪些表 | `internal/app/modules.go`、`internal/app/` 下的 `adapters_*.go`、各模块 `postgres/tables.go` |
| `docs/diagrams/architecture-runtime.html` | 进程、端口、存储和外部依赖怎么连 | `internal/app/api.go`、`internal/app/worker.go`、`internal/app/infra.go`、`deploy/docker-compose.yml` |

改了事实来源里的代码，同一个改动里检查对应的图。图里的每个节点、每条边、每个标签都要能在
代码里找到依据；为了简洁省略的边，写进图下方的说明文字，不要悄悄丢掉。

## 视觉约定

三张图共用一套编码，同一种画法在不同图里含义相同：

| 画法 | 含义 |
|---|---|
| 灰色实线箭头 | 允许的依赖或调用（import、注入、数据库读写） |
| 灰色虚线箭头 + ✕ | 禁止的依赖，违反时 archtest 失败 |
| 灰色虚线箭头（无 ✕） | 可选的依赖 |
| 虚线边框的节点 | 可选的组件（按配置才存在） |
| 蓝色实线箭头 | HTTP 调用（客户端、第三方接口） |
| 点线箭头 | 事件流：发布方 → 消费方；import 方向与之相反 |
| 实线细框、浅底色的容器 | 边界：模块边界或进程边界 |
| 浅灰底、灰框的节点 | 外部参与者，或叶子包（只依赖标准库） |
| 节点左上角的小方框（角标） | 节点的补充分类，如监听端口、注册了哪些入口；用到就要进图例 |
| 红色（焦点） | 每张图最多 1–2 处，标这张图最想让读者看到的东西；可以是节点，也可以是连线（连线保持上面的实线、点线画法，只换成红色），由图例说明含义 |

几条硬性要求：

- **图例覆盖图里用到的全部编码**，包括节点角标；用不到的编码不放进图例；
- **虚线只表示"禁止"或"可选"**，边界一律用实线细框，避免把可选组件看成进程或模块；
- **焦点不超过两处**；
- **箭头方向在一张图里只有一种含义**；含义不同的箭头要用不同画法区分（如依赖和事件流）；
- 连线只走直角折线（转角圆角），标签放在连线旁边、带不透明底色，不压线、不压节点和边界；
  两条线交叉时用跳线；
- 一张图大约不超过 12 个节点，再多就拆图。

## 文件格式

- 单个 HTML 文件，内联 SVG，除 Google Fonts 外不引用外部资源；
- 颜色只用这几个：底色 `#f5f5f5`、节点底色 `#ffffff`、正文 `#2d3142`、次要 `#4f5d75`、
  标签 `#5a6580`、焦点 `#bf4520`、HTTP `#2e5aa8`；浅底色和边框用 `#2d3142`、`#bf4520` 的
  rgba 透明度变体（如 `rgba(45,49,66,0.05)`）；
- 页面标题用 Instrument Serif（中文 Noto Serif SC），节点名称用 Geist，路径、端口、表名这类
  技术内容用 Geist Mono，中文回落到 Noto Sans SC；
- `<svg>` 带 `role="img"` 和 `aria-labelledby`，`<title>` 是第一个子元素，`<desc>` 用一句话
  说清图的内容；改图时 `<title>`、`<desc>` 和图下方的说明文字一起更新；
- 先画连线再画节点，节点盖住线；
- `svg` 的 `min-width` 等于 viewBox 宽度，外层容器横向滚动，手机上不缩小字号。

改完在浏览器里打开看一遍，确认没有文字溢出、标签压线或超出画布。
