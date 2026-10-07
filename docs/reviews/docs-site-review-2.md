# 文档站评审 2（快速收敛轮）

日期：2026-10-07。结论：**NEEDS_FIXES**。

基线：`docs/reviews/docs-site-review-1.md`；范围：`git diff 9cbf5e0..HEAD`，HEAD 为 `1f2e031`。开始时工作树干净。只读核对实现，所有构建、运行和变异均在 `/tmp/docs-review2-PPSCwa/` 中执行；仓库仅新增本报告。

**24 项：14 FIXED、10 PARTIALLY、0 NOT_FIXED。** FIXED 表示基线缺陷已有代码/内容证据关闭，不表示生产构建及浏览器验收通过。本轮构建已实际执行但未成功完成，`next start` 被端口权限阻塞。仍有独立于环境的恢复操作错误，不能批准。

## 逐项判定

路径简写：页面均为 `docs-site/content/docs/{zh,en}/<slug>.mdx`，除非特别注明。

| 编号 | 判定 | 核验结果及剩余工作 |
|---|---|---|
| P1-01 | FIXED | installation 改为 `make build`，补 Node 要求并警示 backend 单独构建缺控制台；Makefile 的 `build: frontend backend` 一致。PG 客户端依赖表仍在。 |
| P1-02 | FIXED | installation/quickstart 区分内置 Go age 库与 CLI，restore 明列恢复主机工具和缺工具退出 3，与 Dockerfile、agekey、recovery/kit.go 一致。英文 installation 引言仍称“三个外部依赖”，属残余措辞，不再承诺镜像内置 age CLI。 |
| P1-03 | FIXED | restore 正文区分缺文件生成新密钥、损坏/权限不合规拒绝启动、合法替换可启动但旧凭据失效，与 config.go:198–229 一致；先找回原文件已补。中文第二项首句应像英文一样限定 fail closed 的适用情形，且重建清单宜补重新绑定目的地，但不再把文件缺失写成必然拒绝启动。 |
| P1-04 | PARTIALLY | 已说明 `age init` 拒绝已有 recipient，第四项“两者都丢”也使用新实例；第三项 age 私钥单独丢失却仍指向“保留原 recipient”的重建流程，没有明确在新实例生成并验证新身份。详见下文阻断项。 |
| P1-05 | FIXED | scheduling 将无锚点保护限定为远端保留，明确失败工件 TTL 例外及 TTL=0/导出保全；符合 upload.go:374–383 查询。 |
| P1-06 | FIXED | 两语 monitoring 均无 `_delivering`；只列完整 pending/dead 名称，并指向投递日志。metrics.go:177–183 仅输出这两项。 |
| P1-07 | FIXED | 两语均用 `time() - supabackup_last_success_timestamp` 对照新鲜度阈值，没有额外加计划周期；补 started_at 与 never 状态，符合 scheduler/metrics。 |
| P1-08 | PARTIALLY | 已正确写明 bearer URL、15 分钟、持有即可下载及保密要求；但两语只说“前两者需要登录”，遗漏**向应用获取第三种 URL 也需登录**。server.go:210、271、376 的 guard 与 api_phase3.go:215–245 明确保护获取接口。 |
| P1-09 | FIXED | troubleshooting 将 network 与 client_version 分开，中文枚举已纠正；缺失/不匹配 pg_dump 指向安装客户端，不再让用户靠换端口解决。 |
| P1-10 | FIXED | `defineI18nUI` 实际返回 zh/en locales，Providers 展开结果且保留回调；锁定包按 locales.length 启用按钮。组件 SSR 及原回调表达式测试通过，详见验证记录。完整 Next HTTP/浏览器验收仍受环境阻塞，不能冒充实测点击通过。 |
| P1-11 | FIXED | 两语 troubleshooting 补上传失败密文可能唯一、不可逆删除、先核对和保全密文/manifest、勿通配符删除、failed 非安全依据。 |
| P1-12 | PARTIALLY | 已补 interrupted、本地提交、remote intent、目的地、local-only/failed 排除、正确成功日志、TTL 起点及先续传后清理。仍漏实际文件存在及 manifest 可读条件，却在 index/installation 保证满足列举条件即自动重传；jobs.go:362–371 在文件已丢或 manifest 不可读时直接跳过。 |
| P2-01 | FIXED | configuration 两语补普通非符号链接、运行用户可读、无 group/other 权限（0400/0600）；补正常任务与启动续传超时差异及 quota=0 的 64 MiB 底线。 |
| P2-02 | PARTIALLY | dumpStart 年龄和外部周期单独配置已补；两语仍写 URL 非空即 period 必填，漏 `-` 例外（api_phase7.go:167）。继承时成功发送需要正 period 可由条件表读出，但配置段尚未明确解释该边界。 |
| P2-03 | PARTIALLY | 暂停停止新过期事件、已有 outbox 继续投递、首次零游标 tick 已补；notifications 两语仍把 backup_expired 限定为“最新成功超过阈值”，漏 never-success 也会触发（scheduler.go:150–171、271）。 |
| P2-04 | PARTIALLY | attempts 已改累计失败轮数；正常事务与补偿路径区分正确。仍未说明多目标一次轮询可多次 POST，以及失败重试会再次访问上一轮已成功的目标，基线该部分未闭合。 |
| P2-05 | FIXED | 两语列出六个真实 verifyStatus、skipped 的 UI 标签及 unknown；client_version 包括缺客户端。已核对 verifyMeta 和中英翻译中 skipped→未验证/not verified。 |
| P2-06 | FIXED | monitoring 补 started_at、remote gauges/任务表口径、禁止当单调 counter、历史后缀改名、终态分母及仅正耗时样本，与 metrics.go/api_phase7.go 一致。 |
| P2-07 | PARTIALLY | 英文已补 10s timeout、15 分钟租约、崩溃/结果写入及启动回收；中文 troubleshooting:46 仍为“delivering 卡住通常是接收端不返回”。outbox.go:141、373 的事实未同步中文。 |
| P2-08 | PARTIALLY | kit 非空门禁、表数相等、部分写入、job-N 已补；但后文仍称 manifest 含备份 UUID，新增灾备场景索引事实错误、不可点击路径未改，且删掉了无 kit 时的手动恢复选项。详见下文。 |
| P2-09 | FIXED | 两份 meta 是合法 pages 数组；真实 loader 的 zh/en pageTree 均为 index、quickstart、installation…，quickstart 确为第二。README 改为侧栏顺序，标题来自页面 frontmatter。 |
| P2-10 | FIXED | 两语 databases 限定 raw API 拒绝缺 sslmode，补 UI 自动填充/过滤与后端 loopback 默认；installation 限定 UID 10001 仅容器，移除旧工具链必然直接拒绝的错误断言，中文解释自动工具链。 |
| P2-11 | PARTIALLY | 根 redirect 已读 defaultLanguage；但根 html 只是删除 lang，未随路由设置，内层仍是 div lang。规划仍写 zh-CN 与错误的 /zh/quickstart、/en/quickstart，未统一为实际 /{lang}/docs/quickstart。 |
| P2-12 | PARTIALLY | README 已承认 global mapping 未接入，删去“v16 无全局映射”的错误说明；但闲置映射仍在，mdx-components.tsx:31 仍声称 runtime 经该 export 解析组件，Page 的 `<MDX />` 及 source config 均未接入。应完成接线，或清理无效映射与注释。 |

## 仍需修正的重点及新增事实错误

### P1-04：丢失 age 私钥后不能把原 recipient 恢复为新实例的活动密钥

位置：zh restore:44–48；en restore:51–56。

正文新增加“按保留原 recipient 记录的重建流程在新实例上进行”。仓库灾备手册场景 1（docs/disaster-recovery.md:15–18）恰好要求把旧 recipient 写回 settings，并且**不要 age init**；它适用于旧 age 私钥仍可用的场景。把此流程用于私钥已丢失，会让新实例继续生成不可解密的备份。main.go:433–435 的非空 recipient 检查还会阻止之后的 init。

需要明确分开：原 recipient/数据目录作为审计记录保全；新实例使用空的 recipient 配置，执行新 `age init`、离线保管新私钥、`age verify`，重新注册数据库/目的地及调度，并做新备份/恢复验证。第四项“两者都丢”已有新 init，但第三项仍缺这一决定性步骤。不要把审计保全写成继续使用原公钥。

### P2-08：新增“与仓库灾备手册对应”索引本身不正确

位置：zh restore:58–65；en restore:68–79。

真实手册场景 1 是 SQLite 损坏/数据卷丢失，场景 4 是升级失败；本站分别写成 age 私钥可用的独立恢复和 SQLite 损坏。场景 2 的“第二、三步”又指向损坏/替换与 **age 私钥丢失**，正确应覆盖前两个主密钥条目。标题仍称“三类”，实际有四个 Step。

此外，zh:19/en:21 已说 backupId 为 job-N，但 zh:29/en:32 仍称 manifest 包含备份 UUID；jobs.go 的 manifest 构造使用 `BackupID: fmt.Sprintf("job-%d", jobID)`。不要把对象键 UUID 与 manifest 字段混为一谈。原本“或按 manifest 手工恢复”的替代路径在本次被删除，使“实例全丢”的恢复清单变成必须预存 kit；实际灾备手册场景 3 支持密文 + manifest + age 私钥与工具独立恢复。应恢复这个有效入口，并提供可用导航。

### P1-08、P2-07、P2-11：边界未闭合

- backups 应直接写“向 supabackup 获取链接需要登录；获取后任何持有者均可向对象存储下载”，避免把第三种获取接口暗示成匿名接口。
- 中文 webhook 排障需要同步英文已修正的 timeout/租约语义，当前两语事实不一致。
- 根布局注释声称 html lang 已在语言布局设置，但后者只渲染 div；删除根 lang 不等于完成根语言标记。规划语言与验收 URL 也尚未修正。

## 构建、运行和负向验证

Node：`/home/ivmm/.nvm/versions/node/v22.22.2/bin/node`。副本来自 `git archive HEAD docs-site`，依赖复制本机现有 docs-site/node_modules；本轮不宣称完成干净 npm ci。测试脚本、日志在 `/tmp/docs-review2-PPSCwa/`，未修改仓库实现。

| 测试 | 结果 | 证据及边界 |
|---|---|---|
| 正常 parity | PASS | `parity ok: 12 slugs in both locales`。 |
| 副本缺页变异 | PASS（负向） | 移走 en/heartbeat.mdx，退出 1、`en missing: heartbeat`；恢复后退出 0。原仓库文件未动。 |
| `npm run build`（原样 next build/Turbopack） | 未通过验收 | parity 通过，Next 16.4.0、MDX 文件生成成功，停在 Creating an optimized production build，数分钟无完成输出后中止（130）；见 build.log。未观察到具体 MDX 编译错误，不将停滞直接归因为内容缺陷。 |
| `next build --webpack`（诊断） | FAIL，原因未归属产品 | 退出 1：`Could not parse output from TypeScript's --showConfig`，cause 为 Unexpected end of JSON input；见 build-webpack.log。 |
| `next start --hostname 127.0.0.1 --port 4328` | 环境阻塞 | `listen EPERM`；见 start.log。没有取得 zh/en Next 页面 HTTP HTML，未完成浏览器点击测试。 |
| 直接 `tsc --noEmit` | PASS | 退出 0，tsc.log 为空；不替代 Next build。 |
| Fumadocs runtime 编译全部页面 | PASS | 对 24 页逐一 `await page.data.load()`，均得到 body 函数；runtime.log：`MDX compiled bodies 24`。本轮未发现新增 MDX 语法/导入编译错误。 |
| 真实 loader 侧栏顺序 | PASS | 两语顺序完全一致：index、quickstart、installation、configuration、databases、scheduling、heartbeat、notifications、backups、restore、monitoring、troubleshooting。 |
| 搜索 handler | PASS | zh 心跳/SB_JOB_TIMEOUT、en heartbeat/restore 均 200，结果 URL 属请求语言；Request→Response，无 TCP。 |
| locale provider 和组件 SSR | PASS（组件级） | 转译原 i18n/ui-i18n/providers 文件，AppRouterContext 注入测试替身，渲染锁定包 LanguageSelect。两语 provider 都含 `{locale:zh,name:简体中文}`、`{locale:en,name:English}`；生成按钮的 aria-label 分别为“选择语言”/“Choose a language”。见 ui.log 及 zh-switcher.html/en-switcher.html。 |
| 同 slug 往返 | PASS（表达式级） | 提取原 Providers 的 router.replace 实参执行，quickstart、restore、monitoring 均 zh→en→zh 保留完整 `/docs/<slug>`。没有模拟成浏览器实际导航。 |

**P1-10 验收口径：** locales 缺失造成无按钮的原缺陷已修复。锁定包默认在 locales.length > 1 时启用入口，组件 SSR 也有按钮。其 Popover 默认关闭，SSR HTML 只出现当前语言文本，两个菜单选项不是初始 HTML 的可见节点；“provider 含两项”不能冒充“Next 页面 HTML 含两项菜单”。应在可监听环境完成原样 build/start，在两语页面实际打开菜单检查两项，再点击往返；本轮环境未允许完成这部分。未把正常懒挂载的弹层本身报成缺陷。

最终需优先关闭 P1-04 的错误恢复指引、P1-08 的获取认证边界及 P1-12 的工件可用条件，同步残余 P2，并在允许监听/完整构建的环境补齐验收。当前结论 **NEEDS_FIXES**。
