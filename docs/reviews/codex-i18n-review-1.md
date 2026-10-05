# supabackup UI / API 国际化评审

## 总体结论

**NEEDS_FIXES**。

评审提交：`3629897`（frontend: add zh-CN / en UI localization）与 `c5c0b7a`（HEAD，backend+frontend: localize API messages via Accept-Language）。按指定范围检查 `git diff f7c3cf2..HEAD`，排除 `frontend/dist` 与 `backend/internal/web/dist`；该区间还包含 `dbb7138` UI 重构，以下明确区分国际化遗漏与已有行为。使用 code-reviewer 技能；未修改业务代码、词典或仓库测试。

发现 **1 项 P1、5 项 P2**。词典 key 和插值变量完全对应，三个现用复数组均正常；OpenAPI 契约及生成文件没有变化，11 条 remediation/pooling 英文逐字保持原样。阻断项是新增代码导致现有检查门禁失败，另有后端漏翻、panic 上下文丢失、语言权重解析及前端局部漏翻问题。未发现本次国际化新增的 XSS、认证绕过或凭据泄漏路径。

Go build、vet、前端 TypeScript/Vite 构建和 api-check 通过。全量 race 测试因沙箱禁止监听端口未能完成，**不能声称 server 包全量 race 稳定**；不监听端口的临时探针及 i18n/platform race 测试通过，详情见验证记录。

## P1 与 P2 编号发现

### P1-01：新增 i18n 模块使前端 lint 失败，且在 render 阶段修改全局 DOM

**证据：** `frontend/src/i18n/index.tsx:69`、`:70`；该文件 `:11`、`:34`、`:38`、`:77`；`frontend/eslint.config.js:17`；`.github/workflows/ci.yml:79`。

`useMemo` 回调执行 `document.documentElement.lang = lang`。这是 render 阶段的外部副作用，不符合纯计算要求；渲染被重复执行或放弃时，DOM 语言可能与最终提交的 UI 不一致。当前 StrictMode 也会重复调用计算。React 官方明确要求 `useMemo` 的计算函数保持纯净：[useMemo 文档](https://react.dev/reference/react/useMemo#parameters)。

**实际验证：** `npm run lint` 退出 1，共 5 个错误：该 DOM 赋值触发 `react-hooks/immutability`；`LANGS`、`getLang`、`translate`、`useI18n` 的混合导出触发 4 个 `react-refresh/only-export-components`。CI 的 Lint 步骤会失败，不能以 `npm run build` 通过替代。

另有同一检查门禁的格式问题：只读 `gofmt -l` 列出 `backend/internal/i18n/i18n_test.go`、`backend/internal/server/api.go`、`backend/internal/server/server.go`；`Makefile:54` 的格式检查因此也不能通过。

**建议：** 将根元素语言同步放入依赖 `lang` 的 effect，保持 memo 纯计算；按现有 lint 约束拆分 React 边界与非组件导出，并修正 Go 格式。验收必须包含前端 lint 和 Go 格式检查。

### P2-01：API 本地化漏掉多条应用自有校验及错误消息

**证据：** `backend/internal/server/api.go:71`、`:74`、`:86`、`:89`、`:123`、`:126`、`:133`、`:148`；`server.go:411`；`api_phase7.go:113`、`:147`、`:229`、`:238`、`:266`、`:374`、`:381`、`:386`（均位于 `backend/internal/server/`）。

将直接 `Message: "..."` 改为 `T` 没有覆盖 `errJSON(...)`、验证函数的 `err.Error()` 和 webhook 的 `detail`。这些包含应用自己的固定文案，并非全部属于应保留英文的历史记录或第三方诊断。

**实测复现：** 使用真实 `Router()` 和 `httptest.NewRecorder`，设置 `Accept-Language: zh-CN`：

| 请求 | 当前结果 |
|---|---|
| `POST /api/auth/login`，请求体 `{} {}` | 400，`request body must contain exactly one JSON value` |
| `POST /api/auth/bootstrap`，token=`x`、username=`a`、password=`123456789012` | 400，`username must be 3-64 characters` |
| 对照：`POST /api/auth/login`，请求体 `{` | 400，`请求体格式错误` |

同一用户流程会出现中英文混排。其他确定的漏翻包括 bootstrap/KDF 过载、登录/注销失败、用户名字符限制、密码长度限制、cron/URL 校验、webhook 名称/事件校验，以及测试投递的 `delivery failed ...` / `receiver returned status %d`。

目的地也有遗漏：`api_phase3.go:117` 返回固定的 `an upload for this destination is in flight`，`:168` 返回 `database not found or a job is active`，原文分别来自 `jobs/destinations.go:196`、`:288`。

**建议：** 在响应边界按已有错误类别/sentinel 区分并翻译应用自有消息；保留需要保留的第三方诊断和持久化英文，继续使用既有脱敏逻辑。逐 handler 清单见后文。

### P2-02：recoverer 在语言中间件外层，中文请求的 panic 响应永远回退英文

**证据：** `backend/internal/server/server.go:142`、`:144`、`:244`；`backend/internal/i18n/i18n.go:31`、`:37`。

注册顺序是 `recoverer → secureHeaders → i18n.Middleware → handler`。语言中间件通过 `r.WithContext(...)` 向内传递新请求，不会修改 recoverer 持有的外层 `req`。因此 recoverer 新加的 `i18n.T(req.Context(), ...)` 看不到语言，仍选择英文。

**实测复现：** 按生产相对顺序执行 `srv.recoverer(i18n.Middleware(panicHandler))`，请求 `/api/probe` 且设置 `Accept-Language: zh-CN`，得到 `{"code":"internal","message":"unexpected internal error"}`。没有改变 HTTP 500 或恢复能力，但预期的中文错误分支不可达。

**建议：** 让语言上下文在进入 recoverer 前建立，同时保留恢复覆盖范围；或让外层错误响应显式从原请求解析语言。增加此顺序的回归测试。

### P2-03：Accept-Language 的 q 解析接受非法权重，并忽略合法的大写 Q

**证据：** `backend/internal/i18n/i18n.go:88`、`:89`、`:95`。

只匹配小写 `q=`，随后直接 `ParseFloat`，既不验证 HTTP qvalue 的范围/语法，也不排除无穷值。实测：

| Header | 当前选择 | 问题 |
|---|---|---|
| `zh;q=2,en;q=1` | zh-CN | 非法的 2 压过合法的 1 |
| `zh;q=+Inf,en;q=1` | zh-CN | 无穷值被接受并胜出 |
| `zh;Q=0,en;q=0.9` | zh-CN | 忽略 Q=0，将明确排除的中文当作默认 q=1 |
| `zh;q=0.1234,en;q=0.1` | zh-CN | 超过三位小数仍接受，说明未按 qvalue 语法校验 |

HTTP 规定 q 名称不区分大小写，权重在 0–1，发送的 qvalue 最多三位小数：[RFC 9110 §12.4.2](https://www.rfc-editor.org/rfc/rfc9110.html#section-12.4.2)。其中大写 Q 的例子不依赖非法输入即可复现。

**建议：** 大小写不敏感解析 q，校验有限值和合法范围/语法；非法项按既有“跳过该 tag”策略处理。新增边界测试。普通浏览器的常规小写头、当前前端发出的单一 `en` / `zh-CN` 不受此问题影响。

### P2-04：若干正常 UI 状态及非品牌标签仍显示英文

**证据与触发：**

| 位置 | 中文界面的遗漏 |
|---|---|
| `frontend/src/lib/providers.ts:56` → `components/RegisterDatabase.tsx:132` | `Self-hosted / other` 被当成“品牌名”直出；它实际是通用分类，还会进入指南标题 |
| `frontend/src/lib/status.ts:85`、`:92` → `components/DeliveryLog.tsx:56` | 合法通知状态 `pending` 落入 default，`StatusBadge` 找不到 key 后仍显示 `pending`；其他三个通知状态已翻译 |
| `frontend/src/components/DatabaseRow.tsx:93` | 已知任务状态直接作为 `{state}` 插值，显示“备份 running…”或“备份 pending…”，没有复用已有任务状态翻译 |
| `frontend/src/components/ui/Skeleton.tsx:4` | `aria-label="Loading"`，读屏用户仍获得英文状态 |

这是系统扫描字符串、JSX 文本、属性以及动态状态来源后确认的遗漏，不是 en/zh 的 key 不对称。通知 `pending` 是契约内状态（`api/openapi.yaml:1046`），不应视作未知 API 值。

**建议：** 增加通用分类和 loading 文案，补全通知 pending 映射，并使用已有 `taskStatusMeta`/词典处理任务状态插值。保留 Supabase/Neon 等品牌、URL 示例、协议枚举和 shell 命令原样。

### P2-05：显式切换 EN 后，绝对时间仍跟随浏览器默认语言

**证据：** `frontend/src/lib/format.ts:24`；`frontend/src/components/ui/RelativeTime.tsx:9`。

`absoluteTime` 中文分支传 `"zh-CN"`，英文分支传 `undefined`。后者表示运行环境默认 locale，不表示英语。因此中文浏览器中选择 EN 后，相对时间变成英语，悬浮绝对时间仍为中文；其他非英语环境也有同类问题。

**验证：** 在 `LC_ALL=zh_CN.UTF-8 LANG=zh_CN.UTF-8` 的 Node 环境中，默认 locale 为 `zh-CN`，以 `undefined` 格式化固定时间得到 `2023年11月15日`。这是对实际 locale 参数行为的复现，未声称做了浏览器端到端测试。

**建议：** 英文分支显式使用 `en` 或选定的英语区域，同时保留用户时区。

## 逐项证据与核查结果

### 1. 词典、复数、detect 与持久化

使用 TypeScript AST 对两份词典的每个属性进行集合、重复及占位变量比对，而非只凭 tsc 推断：

| 项目 | 结果 |
|---|---|
| en key 数 | 310 |
| zh-CN key 数 | 310 |
| zh-CN 相对 en 缺失 | `[]`，无 |
| zh-CN 相对 en 多余 | `[]`，无 |
| 两份词典重复 key | 均为 0 |
| 每个 key 的 `{var}` 集合/次数不一致 | `[]`，无 |
| components/lib 字面量 `t(...)` / `tr(...)` 不存在的 key | `[]`，无；允许复数基 key 对应 `.other` |
| metadata 的 key/label/labelKey/taglineKey | 检查 52 处赋值，未发现缺失；provider stepKeys 亦有对应词条 |

`frontend/src/i18n/en.ts:352` 的 `Record<keyof typeof raw, string>` 和 `zh-CN.ts:4` 的直接对象字面量约束在当前写法下同时检查必需 key 和额外属性。它不约束 `t(key: string)` 的调用方，也不会发现“两份词典一起漏掉 pending”的情况。

`index.tsx:38` 的实际回退顺序是：精确 key → 数字 count===1 时 `.one` → 存在 count 时 `.other` → 原 key。它不是自动英语回退；当前 Dict 全覆盖，未发现因此导致的运行错误。现用复数组为 `health.headline.attention`、`pipe.stage.source.sub`、`pipe.stage.remote.sub`，三组都具备 `.one/.other`，调用处都传数字 count（`HealthSummary.tsx:48`、`PipelinePanel.tsx:113`、`:142`）。

运行真实函数验证 0/1/2：英语 `0 destinations / 1 destination / 2 destinations`，中文分别为 `0/1/2 个目的地`。在内存词典临时去掉 `.one` 后，count=1 正确回退 `.other`；不传 count 返回基 key；未知 key 原样返回。没有改写词典文件。字符串 `"1"` 会走 other，但现有调用均为数字，未列为缺陷。

`index.tsx:24`：合法存储值优先，仅接受 `en`、`zh-CN`；无效值忽略；读取异常回退 navigator；`zh-TW` 等 zh 前缀按既定方案统一到简中，其他语言回退英文。已执行存储优先、无效值、异常、navigator 中文/非中文的探针。`:59` 的 setter 同步更新模块镜像，捕获存储写异常；刷新可恢复持久化选择。没有跨标签页 storage 订阅，也不遍历 navigator.languages；最小方案没有承诺这些行为，不作为阻断。

DOM 副作用不合规及检查失败见 P1-01。

### 2. React 挂载与用户可见字符串

`frontend/src/main.tsx:35` 的单一根结构为 StrictMode → I18nProvider → QueryClientProvider → App。认证页、故障页、Dashboard、ToastProvider 和所有深层组件均在此树中；未发现另一个 createRoot 或树外 portal。`StatusBadge`（`ui/Badge.tsx:23`）与 `ConfirmButton`（`ui/ConfirmButton.tsx:20`）的 useI18n 挂载假设当前成立；独立测试/未来独立挂载时需要包 Provider，不能直接裸挂。

系统扫描范围为全部 `frontend/src/components/`、`frontend/src/lib/`，包含：JSX 文本、label/title/placeholder/tip/aria-label 属性、字符串常量、状态元数据和插值来源。直接 JSX 英文正文只剩品牌 `supabackup` 与 bootstrap 命令；真正的遗漏见 P2-04。其余保留项包括 `production`、`ops-alerts`、`db.internal`、`postgres`、`backup` 等示例值，URL、TLS 模式、Webhook 事件标识、KB/MB/GB 与技术符号；它们不应机械翻译。`lib/format.ts:9` 的相对时间已经显式分语言处理。

`RelativeTime` 自己没有 useI18n，但当前调用者都是会随语言更新的组件，也未做 memo 隔离，现有路径可以随父组件重渲染。没有证据表明它当前不更新；实际绝对时间 locale 问题见 P2-05。

**切换语言的额外限制（未单列阻断）：** `lib/queries.ts:19` 的任务缓存 key 不含语言，`index.tsx:59` 切换时没有失效缓存。`RecentBackups.tsx:90` 的 remediation 要等下一次任务轮询（15 秒）才换语言；`RegisterDatabase.tsx:275` 的 poolingWarning 和已存在的 mutation error/notice 也会保留生成时语言。它们不等于“持久化错误记录必须保持英文”：这是客户端暂存响应的切换延迟。若产品要求切换后全屏即时一致，应明确失效/重取语言相关查询，并处理暂存消息和在途旧语言响应；无需修改历史错误记录。

### 3. 后端路由、guard、ctx 和逐 handler 检查

语言中间件位于根 Router，先于 `/api` 分组的 cookieJar 与 guard（`server.go:144`、`:207`）。普通 `/api` 路由、路由错误处理、guard 的 Content-Type / Origin / 登录 / CSRF / 限流路径均可获得语言；实际 `/api/auth/me` 中文 401 已验证。例外是外层 recoverer，见 P2-02。

比对 `f7c3cf2` 后确认 guard 本来就使用 `req`，并没有把 guard 内的其他变量误改成 req；新改名主要是错误回调从 `_ *http.Request` 改为 `req *http.Request`。guard 仍按原样执行 body cap、same-origin、匿名路由限制、session/CSRF 校验，没有少传请求或改变早退条件。

`cookieJar`（`server.go:550`）、`withUser`（`:536`）和 guard（`:380`）都基于既有请求 context 派生，不会覆盖语言。生成的 strict handler 将 `r.Context()` 传入业务方法（例如 `backend/internal/api/api.gen.go:4324`）。逐一检查四个 api 文件的 T 调用，没有用 Background/TODO 替换请求 ctx，也没有同名 ctx 的错误遮蔽。taskToAPI/dbToAPI 的所有调用均传递当前请求语言。

| 文件 / handler | 核查结果及剩余消息 |
|---|---|
| `api.go:49` GetHealthDetails、`:159` GetAuthMe | 防御性未登录消息已翻译 |
| `api.go:65` PostAuthBootstrap | 必填、账号已存在、token 失败已翻译；username/password validator、KDF busy、bootstrap failed 漏翻 |
| `api.go:100` PostAuthLogin | 必填、长度、错误凭据已翻译；KDF busy、两处 login failed 漏翻 |
| `api.go:141` PostAuthLogout | 服务端注销失败文案漏翻 |
| `api_phase2.go:51` PutAgeRecipient | 必填/格式错误已翻译 |
| `api_phase2.go:89` CreateDatabase | 必填、名称长度、连接测试失败、重名已翻译；URI 校验仅前缀翻译，内层 err.Error 仍英文；中文左括号搭配 ASCII 右括号，仅排版不一致 |
| `api_phase2.go:167` GetDatabase、`:179` DeleteDatabase、`:209` TriggerBackup、`:242` GetTask、`:253` CancelTask | 新改动的 not-found / active / already-queued 消息及语言传递正确 |
| `api_phase2.go:76` ListDatabases、`:229` ListTasks、`:300` taskToAPI | lastTask/remediation 随请求语言；历史 ErrorMessage 原样保留 |
| `api_phase3.go:60` CreateDestination | 缺少 body / 重名已翻译；应用校验与 provider 诊断仍经原来的脱敏后英文输出 |
| `api_phase3.go:109` DeleteDestination、`:121` TestDestination、`:140` ReconcileDestination | not-found 已翻译；删除时 upload-in-flight 漏翻；测试诊断保持原脱敏输出 |
| `api_phase3.go:160` AssignDatabaseDestination | 目的地不存在已翻译；数据库不存在或任务 active 漏翻 |
| `api_phase3.go:217` GetTaskDownloadURL、`:249` DownloadTask、`:283` DownloadRecoveryKit | 本次涉及的 not-found / not-available / not-committed 文案已翻译；下载内容不经词典处理 |
| `api_phase7.go:81` GetDatabaseSchedule、`:93` PutDatabaseSchedule | 固定边界提示和 not-found 已翻译；cron 和 heartbeat URL 内部校验仍英文 |
| `api_phase7.go:223` CreateWebhook、`:244` DeleteWebhook、`:260` TestWebhook | body/重名/not-found/必填/JSON 生成失败已翻译；URL 校验、name/events 校验及 test delivery detail 漏翻 |
| `api_phase7.go:199` ListWebhooks、`:291` ListNotifications、`:326` GetOverview、`:397` GetStats | 无新加需翻译的正常展示消息；枚举不变；通知历史 lastError 保留原文 |
| `server.go:155` 起的路由/strict 错误回调 | 新增 T 调用的语义对应正确；`:193` 生成层参数错误仍 err.Error 英文 |
| `server.go:389` readWholeJSON、`:238` recoverer | 前者漏翻最后一个校验分支；后者上下文错误，见 P2-01/P2-02 |

未发现已成对翻译的消息出现成功/失败、范围、对象类型或操作含义反转。`CreateWebhook` 新内联英文与 `jobs/queries_schedule.go:88` sentinel 文本一致。P2-01 指的是未覆盖的应用消息，不要求翻译所有数据库、网络库的任意英文错误。

### 4. FromRequest 边界与并发

`i18n.go:71` 对 nil/空 header 回退英文；大小写不同的语言 tag 经 ToLower；最高 q 选择和等权保留先到者正常。探针中 `en;q=0.8,zh;q=0.8` 返回 en；`zh;q=0,en;q=0` 返回 en；负权重和 NaN 示例均回退/选择 en，没有 panic。非法正权重和 Q 大小写问题见 P2-03。

当前实现取 `Header.Get` 的首个字段值，不合并重复 Accept-Language 字段；`zh*` 前缀匹配较宽，unsupported/wildcard 的获胜项按注释回退英文。这不是完整 RFC 语言范围协商。按当前“最小方案”的明确 fallback 策略，没有将 `fr,zh;q=0.9` 选择英语单独列作缺陷。

后端语言存放在每个请求 context 中，所有选择变量均为局部变量；没有可被并发请求覆盖的全局 current-language。既有 server 测试扫描没有发现逐字比较英文 message/remediation 的断言，主要比较状态码、code、结构和数据；请求默认也未设置 Accept-Language，所以原有英文 fallback 保持。但现有测试缺少中文路由/guard/recoverer 覆盖，这解释了为什么单包 i18n 测试不能发现 P2-01/P2-02。

### 5. 英文原文、持久化与 platform_test

对 `git show f7c3cf2:...` 的返回字符串和 HEAD 的 `.En` 按分支顺序逐条提取、解码并精确比较：

| 文件 | 比对分支 | 结果 |
|---|---|---|
| `backend/internal/jobs/remediation.go:14` | network、auth、permission、client_version、disk、storage_upload、verification、default，共 8 条 | 英文全部逐字相同，包括空格、引号、标点、命令片段 |
| `backend/internal/platform/platform.go:66` | Supabase 6543、Neon 6543、Neon -pooler，共 3 条 | 英文全部逐字相同 |

PoolingHint 原先无提示的空字符串分支现在均返回空 Msg，判断逻辑未变。`jobs/jobs.go:690` 和 `server/api_phase2.go:162` 日志显式取 `.En`；remediation 在 `api_phase2.go:311` 响应映射时选择语言；历史 `t.ErrorMessage` 在 `:316` 原样输出，没有回写数据库或将历史内容批量变为中文。

`platform/platform_test.go:40` 起的 `.En` 改法保留了原来的“英语警告存在/不存在”的覆盖，未删除场景；但相对于新返回类型的完整语义有所不足：`Msg{En:"", Zh:"错误警告"}` 会通过原来的无警告场景检查，实际 `Empty()` 却为 false；有警告场景也不检查 Zh 非空。建议无警告场景检查 `Empty()`，有警告场景补两种语言断言。这是测试覆盖缺口，当前实现没有出现此错误状态，未单独计为运行缺陷。

### 6. 插值、脱敏、JSON 与 shell

`frontend/src/i18n/index.tsx:45` 是字符串替换，不解释 HTML；所有找到的调用结果进入 React 文本节点/属性，扫描未发现 dangerouslySetInnerHTML、innerHTML 或 eval 消费翻译。用真实 translate 插入 `<img src=x onerror=alert(1)>` 后经 React renderToStaticMarkup，得到 `&lt;img ...&gt;` 文本，没有生成 img 节点。替换采用函数回调，变量中的 `$&` 不会被当成替换模板。此结论限于当前输出路径，不表示 translate 本身是 HTML sanitizer。

新增后端翻译主要是常量。保留的动态拼接有 timezone、ParseURI 校验原因；`api_phase2.go:108` 的 `err.Error()` 拼接在基线已经存在，并非国际化新增。检查 `pgclient/pgclient.go:98` 起的 ParseURI：URL 解析失败返回固定文本，不传播含完整 URI 的 url.Error；仍可能回显 host、port、sslmode、参数名等非密码字段，因此不能把该路径描述成“所有内容都固定或完全脱敏”。本次没有增加 password/原始 connection URI 的响应拼接。

`api_phase3.go:92` 和 `:135` 的访问密钥脱敏保留；连接测试失败仍对客户端返回固定提示（`api_phase2.go:124`）。Webhook test 不把接收方 body 或 URL 拼入诊断（`api_phase7.go:369` 起）。未发现此 diff 扩大敏感数据响应面。

JSON 由 `server/httpx.go:25` 的 json.Encoder 及生成响应编码器序列化，不是手写 JSON 拼接；中文引号/破折号/全角标点不会破坏 JSON。中文消息不流入 shell 执行或 recovery-kit 模板，`DownloadRecoveryKit` 只更改错误响应文案，成功时仍返回原脚本文件。URI 提示括号不配对是显示问题，不是 JSON/shell 注入。

## 验证记录

| 验证 | 结果 |
|---|---|
| HEAD、最近提交、工作区状态 | HEAD=`c5c0b7a`；前一提交=`3629897`；评审开始工作区干净 |
| 指定 diff 与产物排除 | 检查 `f7c3cf2..HEAD`；不评审两处 dist 产物 |
| `make api-check` | **通过**；重新生成后没有契约/生成文件差异；这些文件在基线到 HEAD 也无 diff |
| `npm run build -- --outDir /tmp/supabackup-review-dist`（frontend 下） | **通过**；实际执行 tsc -b 与 Vite，2001 modules；构建产物写入 /tmp，未更新仓库 dist |
| `npm run lint`（frontend 下） | **失败**，5 个错误，全部在新增 i18n/index.tsx，见 P1-01 |
| 变更 Go 文件的 `gofmt -l` | **不通过格式要求**，列出 i18n_test.go、server/api.go、server/server.go；只读检查，没有运行 gofmt -w |
| `GOCACHE=/tmp/supabackup-review-go-cache go build -o /tmp/supabackup-review-bin ./backend/cmd/supabackup` | **通过，退出 0**；出现模块元数据缓存只读警告，但二进制构建成功 |
| `GOCACHE=/tmp/supabackup-review-go-cache go vet ./backend/...` | **通过，退出 0** |
| `go test -race ./backend/...` 首次执行 | 默认 `/home/ivmm/.cache/go-build` 不可写；改用 /tmp 缓存重跑 |
| `GOCACHE=/tmp/supabackup-review-go-cache go test -race ./backend/...` | **未能全量通过：环境阻塞**；jobs/outbox/server 的 httptest.NewServer 因 `socket: operation not permitted` panic；已运行完成的 i18n/platform/auth 等包通过；不是语言消息断言失败 |
| `GOCACHE=/tmp/supabackup-review-go-cache go test -race ./backend/internal/i18n ./backend/internal/platform -count=3` | **通过**，连续三次 |
| 临时 Go overlay 的 `TestReviewI18nPaths` / `TestReviewQBoundaries` | **race 下通过观测断言**；真实 Router 的中文 guard 和漏翻路径、生产相对顺序的 panic 恢复、q 边界均已复现；不监听端口。通过表示复现与当前行为一致，不表示上述缺陷正确 |
| TypeScript AST / Node VM | 310×2 key 逐项比较、插值变量比较、直接/元数据 key 检查、真实 translate 的复数与回退、detect 分支探针通过 |
| React 字符串插值输出 | 实测 HTML 标签 payload 被转义成文本 |
| 英文文本基线比较 | remediation 8/8、pooling 3/3 逐字一致 |
| EN 日期分支 | 中文默认 locale 下复现 undefined 仍输出中文日期 |

临时 Go 探针通过 `/tmp/i18n-review-overlay.json` 映射 `/tmp` 中的测试副本，执行命令为：

```sh
GOCACHE=/tmp/supabackup-review-go-cache go test -race \
  -overlay /tmp/i18n-review-overlay.json \
  ./backend/internal/server ./backend/internal/i18n -run TestReview -v
```

复现值、触发条件和源码位置均已记录于本报告，不依赖临时文件长期存在。没有运行浏览器端到端测试，也没有将受端口限制的全量 server race 测试标记为通过。最终仓库只新增本报告。
