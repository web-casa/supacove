# i18n 修复第二轮复核

## 总体结论

**NEEDS_FIXES**。

基线报告：`docs/reviews/codex-i18n-review-1.md`。评审 HEAD：`c45ba67bd761`（`fix(i18n review r1)`）；检查范围为 `git diff c5c0b7a..HEAD`，并追踪第一轮发现涉及的 handler、校验函数、测试、前端消费者及词典。评审开始工作区干净；使用 code-reviewer 技能。只新增本报告，未修改业务代码、词典或仓库测试；探针、Go overlay、构建产物放在 `/tmp`。

六项发现中 **4 项 FIXED、2 项 PARTIALLY_FIXED**。P1 检查门禁已经恢复。剩余问题为应用校验漏翻、q 参数名未校验，以及修复新引入的 cron 错误文本截断。以下记录 **3 项 P2 待修问题**，不将测试覆盖建议重复计为运行缺陷。

## 逐项判定表

| 第一轮编号 | 判定 | 复核结论 |
|---|---|---|
| P1-01 lint / render DOM 副作用 / Go 格式 | **FIXED** | `npm run lint`、`gofmt -l backend` 实际通过；DOM 同步移入 effect，core 拆分有效。lint 另有明确的 `useI18n` 导出豁免，不能描述成仅靠拆分通过。 |
| P2-01 后端漏翻 | **PARTIALLY_FIXED** | 七个 helper 已接入对应路径，auth、URL、webhook、目的地忙碌等原发现多数关闭；URI 和目的地应用校验仍英文；cron helper 引入双语截断回归。 |
| P2-02 recoverer 顺序 | **FIXED** | 生产 Router 中 i18n 在 recoverer 外，真实 Router 的中文/英文 panic 响应均正确。新增测试只覆盖手工组装链，不能防止 Router 顺序再退化，见测试有效性说明。 |
| P2-03 q 解析 | **PARTIALLY_FIXED** | 第一轮四个反例、合法大小写 Q、数值范围和精度均修复；但 parseQ 丢弃参数名，将任意 `*=数值` 当作 q，不满足新增的严格解析策略。 |
| P2-04 前端漏翻 | **FIXED** | provider 分类及指南、delivery pending、DatabaseRow 状态插值、Skeleton aria 均接入双语词条，运行探针与源码核对通过。 |
| P2-05 absoluteTime 显式 en | **FIXED** | 英文分支显式传 `en`；中文默认 locale 下执行真实函数仍输出英文日期，时区保留。 |

## 尚需修复的发现

### R2-P2-01：cronMsg 截断带空格的表达式，英文响应也回归

**位置：** `backend/internal/server/i18n_msgs.go:61–64`；调用点 `api_phase7.go:113`；错误来源 `backend/internal/scheduler/scheduler.go:93`。

`ValidateCronExpr` 使用 `%q` 保存整个表达式。helper 却在去掉前缀后按第一个空格切分，保留的只是表达式第一个字段及开引号；后四个字段、闭引号全部丢失。这不是特殊的伪造错误，而是正常校验路径可触发。

使用已迁移的临时 SQLite、真实用户/session/CSRF、真实 `Router()` 和 `httptest.NewRecorder`，请求：

```http
PUT /api/databases/1/schedule
Content-Type: application/json
Accept-Language: zh-CN

{"cronExpr":"0 0 31 2 *"}
```

| 语言 | HTTP | 实际 message |
|---|---|---|
| en | 400 | `cron expression "0 has no reachable fire time` |
| zh-CN | 400 | `cron 表达式 "0 没有可达的触发时间` |

原始错误为 `cron expression "0 0 31 2 *" has no reachable fire time`。修复前英文原样返回；修复后两种语言均丢失用户定位问题所需的表达式。JSON 序列化仍有效，缺陷是消息内容损坏。

**建议：** 同时验证固定前缀和固定后缀，只替换这两部分，完整保留中间的带引号表达式；未知模板原样返回。增加包含多个字段、引号/转义的用例，并断言英文与原始错误逐字一致。

### R2-P2-02：第一轮逐 handler 清单中的应用自有校验仍漏翻

**位置：** `backend/internal/server/api_phase2.go:108`、`api_phase3.go:92–97`；来源 `backend/internal/pgclient/pgclient.go:103–137`、`backend/internal/jobs/destinations.go:90–95`、`backend/internal/storage/storage.go:54–89`。

七个 helper 没有覆盖 `CreateDatabase` 的 ParseURI 内层错误，也没有覆盖 `CreateDestination` 的 name / storage.Config 校验。它们在第一轮逐 handler 清单已列出，且属于本应用产生的固定校验文案，不是远端 provider、SQL 或历史记录。

真实 Router、中文请求实测：

| 请求及触发输入 | HTTP | 实际 message |
|---|---|---|
| `POST /api/databases`，`name=test`，`connectionUri=mysql://u:p@example.test/db` | 400 | `connectionUri 不是有效且受支持的 postgres:// URI（URI scheme must be postgres:// or postgresql://)` |
| `POST /api/destinations`，`name=test`，`bucket=INVALID`，提供测试 accessKey/secretKey | 400 | `bucket name is not a valid S3 bucket name (3-63 chars, lowercase letters/digits/dot/dash)` |

同一路径还会返回英文的目的地名称长度、endpoint、region、缺少凭据和 prefix 校验。URI 的缺少 host/user/database、非法 port/sslmode 等应用自有原因也未经过新增 helper。现有脱敏仍保留，未发现此修复扩大凭据输出面。

**建议：** 在上述响应边界翻译可识别的应用校验原因，保留动态技术值；继续脱敏并保留第三方诊断。不能仅因错误来自下层包而全部归入应保留的英文诊断。

### R2-P2-03：parseQ 忽略参数名，非法项仍可参与权重选择

**位置：** `backend/internal/i18n/i18n.go:118–127`，尤其 `:119`；空参数另见 `:90–97`。

`strings.Cut(..., "=")` 的左半部分被赋给 `_`。因此 lower-case 操作没有真正验证名称：`foo=1`、`=1` 都被当作 q=1。根据 [RFC 9110 §12.4.2](https://www.rfc-editor.org/rfc/rfc9110.html#section-12.4.2)，权重参数名是大小写不敏感的 q；Accept-Language 的 weight 语法并不包含任意名称参数。这里按代码自身承诺的“malformed entries skip their tag”评估，而非要求 RFC 对所有无效输入规定唯一响应。

| Header | 实测 | 按当前“跳过非法项”策略应选 |
|---|---|---|
| `zh;foo=1,en;q=0.9` | zh-CN | en |
| `zh;=1,en;q=0.9` | zh-CN | en |
| `zh;q = 1,en;q=0.9` | zh-CN | 严格 weight 语法下 en |
| `zh;,en;q=0.9` | zh-CN | 严格跳过空 weight 时 en |

前两个是参数名误认；后两个补充说明实现仍有宽松接受行为。第一轮的 `q=2`、`q=+Inf`、`Q=0`、四位小数已经正确处理，不能把原有修复描述成完全无效。普通前端发出的单一语言头不受此问题影响。

**建议：** 验证名称确为 q（忽略大小写），并明确空参数、等号两侧空白、额外参数的接受策略；若继续声称严格按语法跳过非法项，应一致拒绝。增加上述反例，避免只测试等号右侧。

## 后端逐 handler 与七个 helper 核对

下表区分源码覆盖和实际执行；未把故障注入未执行的分支声称为已实测。

| 第一轮位置 / 消息 | 本轮结果与证据 |
|---|---|
| `PostAuthBootstrap` username/password 校验 | `api.go:71,74` 调用 `authMsg`；四条精确匹配与 auth 当前常量一致。真实 Router 实测短用户名、非法字符、短密码、129 字符密码全部中文 400。 |
| Bootstrap KDF busy / bootstrap failed | `api.go:86,89` 的 errJSON 参数已使用当前 ctx 的 T；状态、code、英文逐字不变。源码核验，未注入 KDF 饱和及存储失败。 |
| Login KDF busy / 两处 login failed | `api.go:123,126,133` 均已翻译；两处失败（IssueSession 错误、cookie jar 缺失）均保留，状态和 code 不变。源码核验。 |
| Logout 服务端失败 | `api.go:148` 已翻译，保留“会话仍然有效”的语义及错误状态；源码核验。 |
| `readWholeJSON` 多 JSON 值 | `server.go:412` 已翻译；真实 Router 的 `POST /api/auth/login` + `{} {}` 返回中文 400。 |
| `PutDatabaseSchedule` cron | `api_phase7.go:113` 调用 `cronMsg`。解析失败前缀已中文，第三方 cron parser 细节保留英文；不可达时间分支出现 R2-P2-01。两条分支均实测。 |
| schedule heartbeat URL | `api_phase7.go:147` 调用 `webhookURLMsg`，前缀也本地化；真实 Router 实测 ftp URL 返回中文 400。 |
| `CreateWebhook` / `TestWebhook` URL | `api_phase7.go:229,266` 两处均接入 helper。创建路径实测 scheme、host、link-local、metadata endpoint 四条固定原因及 URL parse 错误；测试路径实测 scheme。parse 的应用前缀中文，库细节保留。 |
| `CreateWebhook` name / events | `api_phase7.go:238` 调用 `webhookCreateMsg`；对应 jobs 的两种错误。真实 Router 实测空名称、未知事件均中文 400。重名原有翻译保留。 |
| `TestWebhook` delivery detail | `api_phase7.go:279–282` 在赋值 Detail 前调用 `deliveryDetailMsg`；与 `testWebhookDelivery` 的 request build、网络失败、HTTP status 三种来源逐条对应。helper 实测双语，503 数值保留；未启动外部接收端。 |
| `DeleteDestination` upload-in-flight | `api_phase3.go:117` 调用 `uploadInFlightMsg`，与 `jobs/destinations.go:196` 固定文本精确对应；helper 中文断言通过，未启动实际上传。 |
| `AssignDatabaseDestination` active/not-found | `api_phase3.go:168` 调用 `assignMsg`，与 `jobs/destinations.go:288` 当前固定错误对应；helper 中文断言通过。 |
| `CreateDatabase` URI / `CreateDestination` 应用校验 | 未接入新 helper；真实 Router 复现漏翻，见 R2-P2-02。 |
| 其余第一轮已通过的 handlers | api_phase2 的 age/not-found/任务状态/下载内容，phase3 其余 not-found/下载，phase7 列表/overview/stats 的相关国际化逻辑未改动。历史 task ErrorMessage、notification lastError 仍原样保留。生成层参数错误及第三方诊断未作为新增缺陷计数。 |

### recoverer 顺序及回归测试有效性

`server.go:145` 当前顺序为 RequestID → i18n → requestLogger → recoverer → secureHeaders。recoverer 获得的请求已含语言；原来在其覆盖内的 secureHeaders、guard、handler 仍被覆盖。i18n 移到外层后，其自身不再被这个 recoverer 捕获；当前实现只是请求头解析/上下文构造，未发现可触发 panic 的输入路径。

真实 Router 探针在临时实例中将 auth 依赖置空，使登录 handler 确实 panic。中文得到 500 / `意外的内部错误`，英文得到 500 / `unexpected internal error`。这证明当前生产注册顺序有效。

**测试覆盖建议（非新的 P2 运行缺陷）：** `server_i18n_test.go:32` 手工写死 `i18n.Middleware(srv.recoverer(boom))`，没有调用 Router。通过 `/tmp` overlay 仅将生产 Router 顺序改回旧顺序，再运行原有 `TestRecovererRespondsInRequestLanguage`，测试仍通过。它能保护手工组合的 recoverer 翻译，但不能发现第一轮同类的 Router 顺序回归。建议将已验证的真实 Router panic 场景纳入仓库测试，同时断言两种语言的 500/code/message。

## 前端关闭证据与新增回归检查

### P1-01：拆分、effect、lint

`frontend/src/i18n/core.ts` 只依赖 en / zh-CN 词典，承担语言检测、持久化、翻译和模块镜像；`api/client.ts`、`lib/format.ts` 已改为导入 core。`index.tsx:19–21` 在 effect 中同步 html.lang，依赖为 lang；`:23–33` 的 memo 只构建值，persistLang 和 setState 在返回的 setter 被调用时执行，不在 render 阶段执行。

`frontend/eslint.config.js:23–29` 还对 i18n TSX 的 `useI18n` 导出增加 `allowExportNames`。这是局部豁免，规则并未全局关闭；实际 lint 通过。文件注释“Only components are exported”不完全准确，因为 hook 仍一起导出，但未据此发现运行缺陷。

### P2-04 / P2-05：显示与运行验证

| 项目 | 证据 / 结果 |
|---|---|
| providers.labelKey | `providers.ts:59` 为 generic 设置 key；`RegisterDatabase.tsx:59,133,254,257` 同时覆盖分类、指南 aria 和标题，品牌标签维持原值。探针输出 `去哪里找——自托管 / 其他`。 |
| delivery pending | `status.ts:92–93` 有显式 pending 分支；真实 StatusBadge 在 I18nProvider 下 SSR 输出 `等待投递`，英文为 pending。 |
| DatabaseRow 状态插值 | `DatabaseRow.tsx:93` 先取 taskStatusMeta.label 再翻译，最后插入 backupState。真实 metadata/translate 探针输出 `备份排队中…`、`备份运行中…`；英文保持 `backup pending…`、`backup running…`。 |
| Skeleton aria | `Skeleton.tsx:7` 使用 `ui.loading`；真实组件 SSR 断言中文 `aria-label="加载中"`、英文 `aria-label="Loading"` 通过。 |
| absoluteTime | `format.ts:24` 显式选择 en。`LC_ALL=zh_CN.UTF-8 LANG=zh_CN.UTF-8` 下默认 locale=zh-CN；实际 absoluteTime(1700000000) 英文输出 `Nov 15, 2023, 06:13:20 AM GMT+8`，中文输出 `2023年11月15日 GMT+8 06:13:20`。 |

未执行浏览器端到端测试；SSR 不执行 DOM effect，effect 的位置及依赖由源码与 lint 验证。

### 对称性、循环导入、helper 误配及 errJSON

- TypeScript AST 比对：en / zh-CN 各 **313 key**，缺失、多余、重复均为 0；各 key 的插值变量及出现次数一致。本次增加的三项正是 generic.label、delivery.pending、ui.loading。
- i18n 相对导入图（包含 type-only 边）共四个模块，无循环。关系为 index → core → en / zh-CN，zh-CN 仅 type-import en 的 Dict；没有 core → index/API/format 的反向依赖。
- authMsg、uploadInFlightMsg 的精确字符串匹配与当前错误源相符；未知错误回退原文。webhookURLMsg、webhookCreateMsg、deliveryDetailMsg 的前缀与当前受控来源相符，动态值保留，未发现现有可达路径中的误配。
- **cronMsg 的前缀匹配及切分已造成可达回归**，见 R2-P2-01。它也没有检查期望后缀，不符合文件顶部“未知错误原样回退”的承诺。
- assignMsg 使用 Contains，比精确匹配宽，会丢掉匹配词之外的诊断内容；外层 handler 在本次修复前就使用相同 Contains 分类。目前 domain 返回的是完整固定字符串，未发现另一条现有可达误配；建议改为 sentinel 或完整文本匹配，不据假设另增缺陷。
- errJSON 函数本身未改，六处调用仅用 T 包装消息。逐项核对英文参数、HTTP 状态、code 不变；仓库 server 测试未找到依赖这些英文消息逐字比较的断言。**不能据此声称 server 全量测试已通过**；全量 race 的环境限制见下表。cron 英文改变发生在独立 helper，不是 errJSON。
- `platform_test.go` 保留原场景并加强为空/双语检查，`go test -race` 实际通过；没有删掉原断言来掩盖问题。

## 验证记录

所有命令均在指定仓库执行，前端命令工作目录为 frontend；未执行 gofmt -w。

| 命令 / 检查 | 结果 |
|---|---|
| `git log -3 --oneline`、`git status --short`、指定 diff | HEAD=c45ba67；初始工作区干净；完整检查修复差异及相关错误源。 |
| `npm run lint` | **通过，退出 0**。 |
| `npm run build -- --outDir /tmp/supabackup-i18n-r2-dist` | **通过，退出 0**；执行 tsc -b + Vite，2002 modules。 |
| `gofmt -l backend` | **通过，退出 0，输出为空**。 |
| `GOCACHE=/tmp/supabackup-review-go-cache go build -o /tmp/supabackup-i18n-r2 ./backend/cmd/supabackup` | **通过，退出 0**；有 Go 模块元数据只读缓存警告，未阻止构建。 |
| `GOCACHE=/tmp/supabackup-review-go-cache go vet ./backend/...` | **通过，退出 0**。 |
| `GOCACHE=/tmp/supabackup-review-go-cache go test -race ./backend/...` | **未全量通过，退出 1，环境阻塞**；jobs/outbox/server 的 httptest.NewServer 因 `socket: operation not permitted` panic。不能作为这些包全部断言通过的证据。 |
| `GOCACHE=/tmp/supabackup-review-go-cache go test -race ./backend/internal/i18n ./backend/internal/platform -count=1` | **通过**。 |
| 无端口 overlay 的 `TestReviewR2Router` / `TestReviewR2Helpers` / `TestReviewR2Q`，同时运行现有 recoverer 测试 | **race 下通过观测断言**；确认上述中文路径、实际 cron 截断、漏翻及非法参数接受行为。“通过”表示复现结果与观测断言一致，不表示缺陷行为正确。 |
| Router 顺序变异 overlay + 原有 `TestRecovererRespondsInRequestLanguage` | **仍通过**，证明该测试没有约束生产 Router 顺序；仓库源文件未改变。 |
| TypeScript AST / Node VM / Bun React SSR 探针 | 313×2 词典对称、插值一致、core 无循环；真实日期函数、metadata、翻译与 Skeleton/StatusBadge SSR 检查通过。 |
| `git diff --check` | 通过。 |

探针执行方式：

```sh
GOCACHE=/tmp/supabackup-review-go-cache go test -race \
  -overlay /tmp/i18n-r2-overlay.json \
  ./backend/internal/server ./backend/internal/i18n \
  -run 'TestReviewR2|TestRecovererRespondsInRequestLanguage' -v

GOCACHE=/tmp/supabackup-review-go-cache go test \
  -overlay /tmp/i18n-r2-old-order.json ./backend/internal/server \
  -run '^TestRecovererRespondsInRequestLanguage$' -count=1 -v

LC_ALL=zh_CN.UTF-8 LANG=zh_CN.UTF-8 node /tmp/i18n-r2-front.cjs
/home/ivmm/.bun/bin/bun /tmp/i18n-r2-render.ts
```

首次 Router 探针的 URI 预期子串漏写 `://`，导致探针自身断言失败；按实际固定消息纠正 `/tmp` 探针后重跑通过，未修改被评审实现。修复本报告三项待修问题后，需补充其回归断言并在允许监听的环境完成全量 race 测试。
