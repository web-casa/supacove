# Phase 3 实现评审（当前 HEAD 复核）

评审日期：2026-10-04。范围：`982d01c..5dc7902a9c34`，以当前 HEAD 行号为准。实际 `git log` / `git rev-list --count` 得到 **4 个提交**：`94b7ddf`、`c3341aa`、`ab6ab66`、`5dc7902`，并非需求描述中的 5 个。本报告替换同名旧报告；旧报告针对 `c3341aa` 的结论不能直接套用于本次 HEAD。

依据：`docs/dev-plan.md:54`（协议 C/C.1）、`:63`（协议 D）、`:141`（Phase 3 任务和 DoD）。顺序以规范为准：**intent → upload → 完整性 verify → manifest publish → local commit**。

**结论：未通过。P0：0 项；P1：13 项；P2：7 项。** 本轮仅修改评审文档。故障注入与负向对照放在 `/tmp/codex-phase3-current/`，通过 Go overlay 执行，未修改生产代码和仓库测试。

## P0

本轮未确认需要列为 P0 的缺陷。上一版“全新 SQLite 的相同自增 ID 必然覆盖旧对象”已被新任务的随机 128-bit `backup_uuid` 修复；元数据库快照回滚仍可复用同一个 UUID，属于有明确恢复前提的 P1-01，不能据此宣称 UUID 方案已保证所有恢复场景下的不可变性。

## P1

### P1-01：UUID 防止新任务偶然碰撞，但元数据库回滚仍可覆盖已提交 artifact

- **位置：** `backend/internal/jobs/jobs.go:239`、`:475`、`:660`；`backend/internal/jobs/upload.go:48`、`:75`、`:117`、`:190`；`backend/internal/storage/s3.go:84`；`backend/internal/db/migrations/0007_backup_identity.sql:16`。
- **问题：** UUID 在 Enqueue 时分配，随后存入 SQLite。保存含 pending 任务的元数据快照，任务完成远端提交，再恢复该快照时，同一 pending 任务和 UUID 会重新出现；worker 会再次执行 dump，上传函数则对同一个对象键和 manifest 无条件 PUT。UUID 的随机性不能防止这种身份复用。在无版本化桶中可覆盖原备份；版本化桶中则改变当前版本，当前实现还丢弃 version ID。迁移的 `legacy-<jobid>` 也不是跨实例唯一身份。`jobUUID` 忽略查询错误并允许空值，进一步缺少失败时停止写入的保证。
- **证据：** `TestReviewRollbackPendingUUIDOverwritesCommitted` 用真实 `Enqueue` 和 `BackupNow` 保存 pending 快照，先提交第一份密文，再在独立目录恢复该 SQLite 快照，并向真实 `uploadAndCommitRemote` 输入第二份不同密文；原键内容被替换。此探针验证上传边界和快照身份复用，未声称运行了两次真实 PostgreSQL 导出。
- **可执行修复：** 区分任务尝试与不可变 artifact；恢复 pending/历史任务时先核对远端提交标记及内容身份。已存在相同身份但不同内容时拒绝覆盖并隔离；使用 provider 支持的条件写入/提交标记争用检查。仅对尚无远端身份的旧记录分配新随机 ID，保留已存在的旧键映射。UUID 查询应返回错误，验证非空且只读一次，禁止错误降级为空键。
- **验收标准：** 全新实例、元数据库回滚、复制元数据库的两实例和旧版本升级场景下，均不得覆盖已提交的不同 artifact；相同 artifact 的重试幂等，身份/哈希冲突明确拒绝；UUID 读取失败时没有远端写入。

### P1-02：恢复忽略持久化目的地，解绑后甚至把未上传任务标成成功

- **位置：** `backend/internal/jobs/jobs.go:271`、`:301`、`:332`、`:338`；`backend/internal/jobs/upload.go:34`、`:38`、`:54`；`backend/internal/jobs/destinations.go:278`。
- **问题：** ResumeRemotePhase 读出 job.destination_id，却只检查其是否非 NULL；实际上传重新调用 DestinationForDatabase，读取当前数据库绑定。Interrupted 任务允许改绑/解绑；以后再次恢复时可能把原目的地意图改写为新桶，或在解绑后直接 `return nil`，继而写 `succeeded`，同时保留 `remote_state='uploading'`。这既违反目的地快照要求，也产生没有完成远端阶段的成功记录。
- **证据：** `TestReviewResumeUsesCurrentAssignment` 建立 interrupted/uploading 意图，通过真实 AssignDestination 解绑，再恢复；结果为 succeeded/uploading，远端 PUT 次数为 0。
- **可执行修复：** 在任务进入远端阶段前固定目的地及配置/密文/manifest 引用；恢复只消费该持久意图，不回读可变绑定。区分 local-only 任务与已有 remote intent 的任务，后者不能靠 nil 目的地短路成功；完成更新检查预期状态和 RowsAffected。
- **验收标准：** 在任意远端边界崩溃后改绑、解绑或停用当前目的地，恢复仍只完成原意图或进入可解释的待处理状态；绝不能产生 succeeded/uploading，也不能把旧 artifact 静默提交到新桶。

### P1-03：恢复越过持久化取消仲裁

- **位置：** `backend/internal/jobs/jobs.go:273`、`:320`、`:338`；对照普通成功路径 `backend/internal/jobs/jobs.go:763`。
- **问题：** 恢复查询不排除 cancel_requested=1，转 running 与最终 succeeded 的 SQL 也不检查取消标志。用户已经取消、尚未完成最终状态落库时发生崩溃，重启会再次上传并报告成功。普通 runJob 的取消保护没有复用到新增恢复路径。
- **证据：** `TestReviewResumeIgnoresCancel` 设置 running/uploading/cancel_requested=1，按主程序顺序调用 RecoverInterrupted 和 ResumeRemotePhase，最终状态为 succeeded。
- **可执行修复：** 在统一状态机中仲裁取消和远端恢复；成功更新必须带取消条件并检查更新行数。已经存在的远端副本可以保留、对账，但不能把被取消的任务改写为成功。
- **验收标准：** 对 intent、上传、verify、manifest、local commit 前后注入取消与崩溃；取消记录不会被恢复覆盖为成功，副本归属始终可追踪。

### P1-04：启动恢复没有按远端事实补提交，一次暂时失败后永久失去自动重试

- **位置：** `backend/internal/jobs/jobs.go:273`、`:304`、`:312`、`:332`、`:334`；`backend/internal/jobs/upload.go:54`、`:75`；`backend/internal/jobs/jobs.go:748`。
- **问题：** 恢复仅接受 interrupted，要求本地密文和 manifest 都在，然后无条件完整重传，即使 remote_state 已经 committed。远端完整提交但本地文件缺失时无法只补本地状态；远端提交已有标记时重传还会在旧标记存在期间覆盖其密文。恢复遇到暂时故障会通过 fail 改成 failed，下次启动不再选中；普通上传耗尽三次重试或 manifest/GET 暂时失败也进入这一死路。没有后续复用原 artifact 的恢复队列/API；重新 Enqueue 会新建任务并重新 dump。
- **证据：** `TestReviewResumeTransientErrorBecomesPermanent` 令前三次 PUT 失败，后端随后恢复；第二次启动恢复没有再发请求，任务保持 failed/uploading。`TestReviewResumeCommittedStillRequiresLocal` 先真实完成远端提交，删除本地密文并模拟重启，任务仍滞留 interrupted。
- **可执行修复：** 按意图查询并校验远端 manifest、对象及版本；已提交者补本地状态，未提交者重传同一 artifact。持久化可重试阶段、错误类别、次数和下次尝试时间，覆盖 failed/uploading；遇到已有提交标记不盲目重写。上传意图之前的已提交本地产物也应有明确的续传入口。
- **验收标准：** 每个外部副作用前后崩溃后均幂等收敛；远端已经提交时不依赖本地文件补成功；暂时失败恢复后自动继续，pg_dump 次数仍为 1；有旧提交标记时不产生“标记存在、对应密文被重新覆盖”的窗口。

### P1-05：锚点资格仍只认 committed，未验证历史记录可以淘汰最后一份合格备份

- **位置：** `backend/internal/jobs/upload.go:233`、`:260`、`:273`；`backend/internal/db/migrations/0006_destinations.sql:37`。
- **问题：** 当前上传已强制读回，但 retention 仍仅按 committed/id 排序，把最新记录直接视为锚点，不检查 remote_verified，也没有持久、事务替换的锚点。前两个实现提交能产生 committed/remote_verified=0 的合法旧状态，迁移 0007 没有修复或隔离。已有最新未验证记录时可删除更老但已验证的最后一份；新增测试夹具也默认制造这种状态。
- **证据：** `TestReviewLegacyUnverifiedAnchor` 预置一个 verified=1 的旧副本和一个 verified=0 的较新 committed 副本，keep=1 清理后旧副本被标 deleted。该证据针对升级遗留状态，不再把普通新上传误报成“跳过 verify”。
- **可执行修复：** 显式定义完整性锚点资格并持久化，在事务内选取/替换；未通过完整性校验的旧 committed 对象重新验证或隔离。无合格锚点时停止破坏性自动清理并记录告警。当前 beta 全量模式也必须落实上述要求；后续新增恢复域/部分模式时再扩展分域。
- **验收标准：** 混合已验证、未验证、失败和升级旧记录运行 count/day retention，最后一份合格锚点不能被未验证对象替换；无锚点时 DELETE 调用为 0。

### P1-06：删除没有 deleting 状态和确认，部分删除后仍宣称 committed

- **位置：** `backend/internal/jobs/upload.go:280`、`:285`、`:289`、`:307`；`backend/internal/server/api_phase3.go:189`。
- **问题：** 删除密文之前没有先落库删除意图；密文删除成功而 manifest 删除失败、进程崩溃或 SQLite 更新失败时，记录继续保持 committed。下载接口据此签发指向不存在对象的 URL，对账也只报告缺失。重试只依赖下一次成功备份再次触发 retention；没有启动时收敛删除、逐对象结果持久化或删除确认。
- **证据：** `TestReviewPartialDeleteAndLogLeak` 注入 manifest DELETE 失败，确认密文已消失而远端状态仍为 committed。
- **可执行修复：** 原子检查锚点/活动引用后持久化 deleting，再逐对象/版本删除并记录结果，确认全部完成才进入 deleted。独立清理调度恢复未完成删除；下载与可用性查询排除 deleting。清理失败保留副本引用和可重试错误。
- **验收标准：** 密文/manifest 任一 DELETE 前后及最终 SQLite 更新前后杀进程，重启可重复执行并收敛；不会把部分缺失副本视为 committed/可下载；锁定对象拒删和逐对象部分失败可明确查询。

### P1-07：忽略 version ID，B2 和版本化 S3 的保留并未真正删除历史内容

- **位置：** `backend/internal/storage/storage.go:107`；`backend/internal/storage/s3.go:84`、`:99`、`:111`、`:122`、`:142`、`:155`、`:181`；`backend/internal/db/migrations/0006_destinations.sql:29`。
- **问题：** Put 的返回类型只有 error，丢弃普通 PUT 和 multipart 返回的 version ID；GET、DELETE、Presign 仅接受 key，schema 也未保存版本。B2 不带 versionId 的 DELETE 会创建 delete marker，而不是永久删除版本；当前列表看不到被隐藏版本，代码却标记 deleted、清空引用。重传形成的旧版本及诊断 canary 同样可能持续占用空间。B2 官方明确区分这两种删除语义：[S3 Delete Object](https://www.backblaze.com/apidocs/s3-delete-object)。
- **可执行修复：** 上传结果返回并保存密文/manifest 的可用 version ID；校验、下载和删除绑定相应版本。对支持版本化的 provider 盘点已确认归属的历史版本/marker，逐版本删除并确认；按 provider 明确区分无版本对象语义。不要用“当前 key 不可见”代替“版本已释放”。
- **验收标准：** 真实 B2 和开启版本化的 S3 中反复上传/重试/删除后，仅留下策略允许的版本；版本锁定/权限拒绝不会标 deleted；双游标版本分页、历史版本和 marker 均被覆盖。

### P1-08：失败/未提交产物没有宽限期和数量/字节预算

- **位置：** `backend/internal/jobs/upload.go:236`、`:346`；`backend/internal/jobs/jobs.go:753`、`:793`；`backend/internal/db/migrations/0006_destinations.sql:29`。
- **问题：** remote retention 只选 committed，本地 prune 只选 succeeded，两者只在普通任务成功后触发。上传、读回或 manifest 连续失败留下的本地密文及 uploading 远端对象既不进入恢复队列，也没有独立宽限期和预算。已有 staging 全局配额/空间底线最多让后续备份停止，不能替代协议 D 要求的失败产物治理和保护冲突告警。
- **可执行修复：** 建立失败/待定副本的持久队列，记录宽限期限、数量/字节占用、活动引用和恢复尝试；清理不依赖下一次备份成功。预算与锚点/待重传保护冲突时暂停删除并显式告警，不突破保护。
- **验收标准：** 连续 30 次 PUT/GET/manifest 故障后，副本处于有界清理策略或明确的保护冲突暂停状态；占用、重试、宽限期和告警可查询，待重传密文不会被误删。

### P1-09：目的地删除仍漏过导出阶段，分配检查与删除也未原子协调

- **位置：** `backend/internal/jobs/jobs.go:246`；`backend/internal/jobs/destinations.go:180`、`:187`、`:205`、`:269`、`:278`；`backend/internal/jobs/upload.go:54`、`:180`。
- **问题：** DeleteDestination 的新 guard 检查 jobs.destination_id + pending/running，但 Enqueue 根本不写 destination_id，直到远端 intent 才补。因此真实 pending/dump 阶段仍可成功删除当前目的地，随后任务无法解析目的地。删除还保留 databases 的绑定，使后续备份继续失败；GetDestination 排除软删除行，使历史备份的 BuildBackendByID/下载也失效。AssignDestination 对活动任务的条件 UPDATE 是原子的，但目标存活先 GET、后 UPDATE，期间可被软删除，最终绑定失效行。
- **证据：** `TestReviewDeleteDestinationBeforeIntent` 使用真实 Enqueue，切入 running 后删除目的地成功，随后 DestinationForDatabase 返回 ErrDestinationNotFound。其余窗口由实际读写顺序确认。
- **可执行修复：** 入队/领取阶段事务性固定目的地引用和配置快照；删除及分配在同一事务或条件 SQL 中检查目标存活和所有活动引用。区分“停用新写入”与“移除历史访问配置”，保留历史下载/清理需要的配置；删除时明确处理数据库绑定。retention 使用任务快照而非成功后再次读取当前绑定。
- **验收标准：** 用 barrier 覆盖 enqueue/dump、读取目的地到写 intent、assign 存活检查到 UPDATE 的竞争；没有悬空绑定，既有任务使用原配置或在副作用前明确拒绝，停用后历史备份仍可读取。

### P1-10：Uploader 所有权已修复，但取消仍无法 abort，进程崩溃残留也无回收途径

- **位置：** `backend/internal/storage/s3.go:95`、`:104`；`backend/internal/jobs/upload.go:75`；钉定 SDK `feature/s3/manager@v1.23.11/upload.go:874`、`:884`。
- **问题：** 现在仅由 Uploader 创建 multipart，原来的双 upload ID 泄漏已修复。然而 SDK fail 使用原 `u.ctx` 发送 AbortMultipartUpload，且吞掉 abort 错误；取消/超时后请求无法发出。应用仅把真实 UploadID 拼入错误文本，没有独立清理 context、持久清理记录或崩溃后的 multipart 盘点。SIGKILL 更不会执行错误收尾。官方对失败 UploadID 的入口是 MultiUploadFailure：[AWS Go v2 S3 utilities](https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/sdk-utilities-s3.html)。
- **证据：** 当前 SDK 传输探针 `TestReviewCanceledMultipartCannotAbort` 得到 **create=1、abort 请求=0**；不再沿用旧报告 create=2 的结果。
- **可执行修复：** 保持唯一 Uploader 所有权；失败时用 MultiUploadFailure 的真实 ID，在独立、限时 context 中兜底 abort，并持久记录仍无法回收的 ID。补充归属明确的崩溃残留盘点或明确、经验证的 multipart lifecycle 配置指引。调用 Put 时传已知 artifactSize，落实声明的 64 MiB 阈值。
- **验收标准：** part/complete 失败、取消、超时和 SIGKILL 后，ListMultipartUploads 中不存在未纳入回收机制的遗留；清理失败可观测、可重试，不影响其他实例上传。

### P1-11：远端 manifest 仍引用本地任务身份，对账没有实现丢失 SQLite 后的 manifest 发现

- **位置：** `backend/internal/jobs/jobs.go:688`、`:714`；`backend/internal/jobs/upload.go:48`、`:117`；`backend/internal/jobs/reconcile.go:50`、`:64`、`:110`。
- **问题：** 对象键已改为 UUID，实际发布的 manifest 却仍是 `backupId=job-N`、`archive.fileName=backup-jobN.dump.age`；不包含准确的远端对象键和版本引用。格式版本字段虽然存在，但不能弥补身份不一致。Reconcile 仅比较 LIST 和 jobs 字符串，从未读取/解析 manifest；SQLite 丢失后，所有旧备份仅显示 orphaned key，不能提供 Phase 3 要求的自描述只读发现。
- **可执行修复：** manifest 使用持久不可变 backup UUID，并保存真实密文对象引用/大小/完整密文哈希/可用版本及恢复信息；发布前固化其内容。对账增加受大小/格式限制的 manifest 目录解析，区分可发现、归属已确认、完整性已验证；未知旧前缀始终保持只读。
- **验收标准：** 完全移除 SQLite 后，仅凭桶与读取凭据可发现原备份，准确找到密文并下载校验；不同实例的 job-1 不会混淆；损坏/陌生 manifest 明确标记，不能自动成为锚点或删除候选。

### P1-12：诊断 DELETE 失败被 defer 吞掉，无删除权限也能通过创建测试

- **位置：** `backend/internal/storage/s3.go:194`、`:203`、`:207`、`:228`；`backend/internal/jobs/destinations.go:104`。
- **问题：** DiagnosticTest 返回的是未命名 error；defer 修改局部 `result` 不会改变已求值的返回值。写/读成功但 DELETE 返回 AccessDenied 时，最终仍返回 nil。已有主错误时 `result == nil` 条件也无法可靠合并清理错误，且无日志。目的地创建于是接受了不能执行 retention 的凭据，诊断 canary 留在桶内。
- **证据：** `TestReviewDiagnosticDeleteFailureReturnsSuccess` 通过真实 AWS SDK 的 HTTPClient 注入 PUT/GET 成功与 DELETE 403，观察到一次 DELETE、函数返回 nil。
- **可执行修复：** 使用命名返回错误或显式清理流程，将主错误与清理错误安全合并；独立短超时 context 可以保留。成功标准必须包括删除及必要确认；返回固定 API 文本，详细错误经过脱敏保存。
- **验收标准：** 写、读、校验、删除分别失败的测试都验证最终结果和桶内残留；DELETE 拒绝时创建/测试接口返回 422，不能返回成功；双重故障保留主因和清理失败信息。

### P1-13：目的地错误出口未统一脱敏，422 也没有固定文本

- **位置：** `backend/internal/jobs/jobs.go:574`、`:618`、`:753`、`:333`；`backend/internal/jobs/upload.go:289`；`backend/internal/server/api_phase3.go:91`、`:94`、`:134`、`:145`、`:201`。
- **问题：** runJob 的 knownSecrets 只有 PG 密码，目的地凭据没有加入；新增恢复日志、retention、reconcile、presign 直接输出原始错误。创建和诊断虽然调用 Secrets，但 422 仍返回不可信 provider 错误文本，不符合本次固定文本要求；仅按已知秘密替换也不能保证整个签名 URL 被消除。没有发现成功生成的 URL 被主动记录：缺口是错误出口，而不是正常返回路径。
- **证据：** `TestReviewPartialDeleteAndLogLeak` 将目的地 secret 设置为测试标记并注入含该值的删除错误，标记原文进入生产 retention 日志。此为错误回显故障注入，不表示真实 provider 正常响应一定含 secret。
- **可执行修复：** 在持有目的地凭据的统一边界构造可安全记录/持久化的类型化错误，复用现有脱敏逻辑并覆盖编码形式；签名 URL 整体视为 bearer secret。两个诊断 422 返回固定 code/message，错误详情仅进入受控、脱敏的诊断记录，其他出口不再附原始 error。
- **验收标准：** 对 PUT/GET/manifest/DELETE/LIST/PRESIGN 注入原文、URL 编码、JSON 转义凭据和完整签名 URL；原始日志、SQLite error_message、任务 API 与诊断 API 中均无秘密或有效 bearer URL；422 文本不随 provider 错误改变。

## P2

### P2-01：远端保留覆盖本地策略，unlink 失败仍丢失引用

- **位置：** `backend/internal/jobs/upload.go:294`、`:300`、`:307`、`:375`、`:383`；`backend/internal/jobs/jobs.go:795`。
- **问题：** localKeep=10、keepRemote=1 时，刚由本地策略保留的旧文件会被远端 retention 随后无条件删除。两条本地清理路径在 os.Remove 失败、路径未通过检查或查询失败后，仍可能清空路径；文件未释放，但正常清理链路已失去引用。简单 HasPrefix 也不是文件系统路径遏制。
- **证据：** `TestReviewRemoteRetentionOverridesLocalKeep` 复现 10/1 配置；`TestReviewLocalPruneDropsFailedUnlinkReference` 通过不可删除的非空目录注入失败，确认路径仍存在但 DB 引用被清空。
- **可执行修复：** 本地与远端副本分别选择候选、维护状态；每个文件仅在删除成功或确认不存在后清空引用，其余保存 cleanup error 和重试信息；统一使用受 staging 根目录限制的文件操作。
- **验收标准：** 10/1、1/10 及 keepDays 组合严格遵守各自策略；权限/IO 错误后路径和错误可查询、可重试，不虚报空间释放。

### P2-02：下载没有活动引用保护，刚发放的 URL 可立即失效

- **位置：** `backend/internal/server/api_phase3.go:189`、`:198`、`:224`、`:244`；`backend/internal/jobs/upload.go:273`、`:375`。
- **问题：** 签发 15 分钟 URL 时不登记租约，retention 也不排除下载引用；下一份备份成功即可删除刚签名的旧对象，破坏用户开始/重试/分段下载。本地下载从读路径到 os.Open 之间同样存在被 prune 的窗口。已经打开的 Linux 普通文件不会仅因 unlink 必然中断，此处不作该扩大结论。
- **可执行修复：** 签名前原子检查副本状态并登记覆盖 URL 生命周期的保护租约；清理排除未过期租约。协调本地打开/引用登记与清理，下载结束或租约过期后释放。
- **验收标准：** 签发旧备份 URL 后立刻运行 keep=1 retention，仍能在承诺窗口内开始/重试下载；本地下载与 prune 的 barrier 测试不出现路径读取后的意外删除；过期引用可回收。

### P2-03：Reconcile 分类重叠、诊断过滤错误，并发变化可伪装成确定缺失

- **位置：** `backend/internal/jobs/reconcile.go:50`、`:58`、`:64`、`:100`、`:110`。
- **问题：** uploading 对象进入 Uncommitted，却未加入 expected，因此同时进入 Orphaned。`strings.Contains(key,"/diagnostic/")` 漏掉空 prefix 下的 `diagnostic/...`，也会把合法 prefix `team/diagnostic/` 下的全部正式备份排除。LIST 先于数据库查询，worker 在二者之间提交的新对象会被误判 missing；没有稳定快照/二次核对，也未检查迭代结束的 rows.Err。
- **证据：** `TestReviewReconcileOverlappingCategories` 确认同一有引用的 uploading 对象同时出现在两个分类；过滤及快照窗口由代码直接确认。函数保持只读，所以这里是报告正确性问题，不是对账直接误删。
- **可执行修复：** 所有已知引用都纳入归属集合，各分类互斥；仅匹配规范化 prefix 后紧接的 diagnostic namespace。检测对账期间状态变化并二次核对，或标成“扫描期间变化/待确认”；检查 rows.Err。
- **验收标准：** 空 prefix、含 diagnostic 路径段的合法 prefix、上传/删除与多页 LIST 并发均有测试；无分类重复，扫描竞争不能输出未经复核的确定缺失。

### P2-04：本地下载仅做词法检查，符号链接可越出 staging

- **位置：** `backend/internal/server/api_phase3.go:232`、`:240`、`:244`、`:249`。
- **问题：** filepath.Abs 只规范化路径，不解析或约束符号链接；staging 内链接指向外部文件时，os.Open 会读取该目标。触发前提是能布置 staging 文件/链接或恢复了含此类路径的状态；未发现普通 API 能任意写入该路径，因此不定为无需前提的远程文件读取漏洞。
- **证据：** `TestReviewDownloadFollowsSymlinkOutsideStaging` 使用真实 DownloadTask，staging 内链接成功返回根目录外的测试内容。
- **可执行修复：** 使用当前 Go 支持的 os.OpenRoot/os.Root 打开 staging 中的相对路径，限制链接解析不得越界，并仅允许普通 artifact 文件；不能只加一次 EvalSymlinks 后再用普通 Open 留下竞态。
- **验收标准：** 绝对/相对越界、文件和父目录符号链接、链接替换竞争均不能读取根目录外内容；正常已提交文件及流结束关闭文件句柄行为保持正确。

### P2-05：两项现有测试没有命中目标分支，MinIO 测试也不足以支持完整性与分页结论

- **位置：** `backend/internal/jobs/phase3_test.go:389`、`:410`、`:416`；`backend/internal/storage/minio_test.go:131`、`:141`、`:154`；`backend/internal/jobs/phase3_test.go:305`。
- **问题：** TestUploadVerifyMismatchDeletesRemote 仍检查旧 `dest/databases/...` 键，所以即便 UUID 对象没有被删除也会通过。TestUploadFailureRetainsArtifact 把失败注入旧键，同时提供错误 hash，实际依靠 verify 失败通过，未验证 PUT 失败/三次重试。TestMinIOEndToEnd 计算 hash 却不与输入比较，列表仅约 3 个对象，预签名仅检查 URL 含桶名；TestUploadProtocolCCommittedPipeline 实际直接调用远端阶段，没有运行其注释宣称的完整 runJob。
- **证据：** 本轮用 overlay 移除确认损坏后的 Delete 调用，这两个仓库测试仍同时 PASS，详见 `/tmp/codex-phase3-current/negative-control.log`；生产代码未被修改。
- **可执行修复：** 统一从持久化 UUID/对象引用构造测试键；失败测试使用正确 size/hash，断言具体失败类、调用次数、对象状态和 artifact 未变。MinIO 断言 SHA-256 相等，真实遍历 >1000 个对象、实际 GET 预签名 URL；修正测试命名/注释并补完整调用链。
- **验收标准：** 删除必要清理、禁用重试、提前发布 manifest、破坏分页 token 或返回同大小错误内容时，对应测试必须失败；正常实现通过且明确区分 fake、SDK 传输和真实 provider 测试。

### P2-06：provider 能力声明和逐家验收未完成，阈值还与实际上传路径不一致

- **位置：** `backend/internal/storage/storage.go:53`；`backend/internal/storage/s3.go:19`、`:47`、`:83`；`backend/internal/jobs/upload.go:75`；`backend/internal/storage/minio_test.go:98`；`docs/dev-plan.md:146`。
- **问题：** 实现有 R2 region=auto、B2 必填 region 等预设，但没有逐家声明小对象/分片 checksum 策略与 SDK 配置、版本/删除语义及分页上限；也没有三家实测证据和 multipart/桶生命周期说明。jobs 总传 size=-1，绕开声称的 64 MiB 单 PUT 阈值，Uploader 按 16 MiB partSize 决定 multipart。强制完整 GET/SHA-256 是正确的 C.1 兜底，但不能证明所有 API 组合兼容。R2 官方兼容矩阵明确区分 FULL_OBJECT/COMPOSITE checksum，并未实现 S3 PutBucketVersioning：[R2 S3 API compatibility](https://developers.cloudflare.com/r2/api/s3/api/)。不能把其能力与 B2/S3 版本化语义统一假定。
- **可执行修复：** 为每个 provider 固定并记录 SDK checksum 配置、阈值、版本和删除能力；传正确大小，说明元数据哈希和 ETag 不构成完整性验证。补实际 AWS/R2/B2 的可重复测试入口和证据，配置 lifecycle 风险提示；无凭据/无环境时标未执行而非成功。
- **验收标准：** 三家分别通过小对象、multipart、取消回收、同大小篡改/截断/缺 checksum、>1000 分页、下载、锁定/权限错误和适用版本删除；声明与实际请求一致，生命周期不被描述成工具可保证的锚点保护。

### P2-07：verifyReadback 的默认值和语义与接口不一致

- **位置：** `api/openapi.yaml:846`；`backend/internal/server/api_phase3.go:77`、`:266`；`backend/internal/jobs/upload.go:97`。
- **问题：** OpenAPI 默认 true，handler 省略字段却保存 false。当前强制 verify 已消除了此前的完整性漏洞，但这个选项实际上不控制 read-back；注释改称控制恢复验证调度，当前实现又没有消费它的调度逻辑。API 返回的 false 会误导调用方，不能用未来功能解释现有字段。
- **证据：** `TestReviewMissingVerifyReadbackDefaultsFalse` 确认省略字段得到 false；`TestReviewProtocolOrderAndReadbackMandatory` 确认 false 仍执行完整 read-back。
- **可执行修复：** 明确当前字段契约：完整性 read-back 无条件执行；删除/弃用无效开关，或将未来恢复验证选项单独定义。仍保留默认值时正确处理 nil，更新 OpenAPI、生成类型和说明。
- **验收标准：** 省略/true/false 有一致、清楚的返回与执行语义；任何选项都不能跳过 C.1 必要校验，客户端不会看到虚假的功能状态。

## 已确认的修复与安全边界

- 普通上传路径已满足 intent → ciphertext PUT → 完整 GET/SHA-256 → manifest PUT → local commit，read-back 不再受 false 开关绕过；网络读错误不会被直接判损坏删除。顺序和同大小内容篡改均有本轮探针验证。
- manifest 使用 bytes.NewReader，旧版 HTTP endpoint 的非 seekable reader 问题已修复。Uploader 不再额外创建一个无关 upload ID。
- 新入队任务的随机 UUID 不再依赖 SQLite 自增 ID；用户 prefix 拒绝 `..`、NUL、反斜杠、百分号等注入字符，并规范化边界斜杠。未发现当前 API 输入可直接构造跨 namespace 的 `../` 对象键。UUID 恢复语义仍见 P1-01。
- endpoint 正则拒绝 userinfo、路径、query/fragment 等；自定义 HTTP endpoint 是现有 MinIO 使用能力。未把管理员配置内网 S3 地址本身误报为未经授权 SSRF。端口范围、IPv6 支持等校验细节尚可完善，但不是本轮主要阻断点。
- SecretKey 使用既有 AES-GCM 加密函数，创建/列表的正常成功视图不包含 SecretKey/AccessKey；requestLogger 只记 path，不记返回的签名 URL 或响应体。异常文本仍需 P1-13 的统一处理。
- destBackends 的 map 访问受锁，默认 factory 已在 NewRunner 初始化，发布缓存时有二次检查；本轮 32 路并发默认 factory 探针经 -race 通过并返回同一缓存实例。不再报告旧版首次初始化 race。SetBackendFactory 的安全前提是其文档规定的启动前调用，不能据此宣称支持运行期热切换。
- AssignDestination 对同库 pending/running 的 SQL guard 有效，清空写 SQL NULL 的旧缺陷已修复；目标存活与删除的竞态仍见 P1-09。
- 列表使用 SDK paginator，本轮传输探针确认 continuation token 能进入第二页；未据此宣称真实 >1000 对象验收完成。Reconcile 没有远端写/删操作。

## 验证结果与具体补测清单

环境：Go 1.26.0 / linux-arm64。默认 Go 缓存目录只读，改用 `GOCACHE=/tmp/codex-phase3-gocache`。

| 验证 | 本轮结果 |
| --- | --- |
| `go build ./backend/...` | 使用上述 GOCACHE 后通过。 |
| `go test -race ./backend/...` | **未全量通过，受环境限制**：server 测试的 httptest 监听 `[::1]:0` 被沙箱拒绝（socket: operation not permitted）；不记成生产代码断言失败，也不记成全绿。 |
| 其余 11 个含测试包 `go test -race -count=1` | agekey/auth/config/crypto/db/dumper/jobs/limiter/pgclient/redact/storage 均通过；storage 中 MinIO 被跳过。 |
| 当前 HEAD 定向 overlay 探针 | **19 项通过，无 race 报告**；其中缺陷探针以“复现缺陷”为通过条件，不能理解成修复验收通过。server 探针直接调用 handler，无需监听端口。 |
| 负向对照 | 移除损坏对象清理后，TestUploadVerifyMismatchDeletesRemote 与 TestUploadFailureRetainsArtifact 仍通过，证实测试漏检。 |
| `bash -n` | 2 个脚本通过。 |
| Python `yaml.safe_load_all` | 4 个 YAML 文件解析通过；这里只验证语法，不代替 OpenAPI 语义/客户端契约检查。 |
| Docker / MinIO | docker.sock 权限拒绝；sudo 受 no-new-privileges 限制。单独运行 TestMinIOEndToEnd 明确 SKIP。未实测 MinIO/AWS/R2/B2。 |

可复跑的本轮探针命令（文件位于当前工作环境 `/tmp`，不属于仓库交付）：

```bash
GOCACHE=/tmp/codex-phase3-gocache go test -race \
  -overlay=/tmp/codex-phase3-current/overlay.json \
  ./backend/internal/jobs ./backend/internal/storage ./backend/internal/server \
  -run '^TestReview' -count=1 -v
```

关键证据：`/tmp/codex-phase3-current/probes.log`、`packages.log`、`negative-control.log`、`minio.log`；全量测试受限记录：`/tmp/codex-phase3-race2.log`。

| 生产函数/现有测试 | 应补的具体测试函数及断言 |
| --- | --- |
| uploadAndCommitRemote / ResumeRemotePhase | `TestRemoteCommitCrashBoundaries`：每个外部副作用前后子进程强制退出再启动；核对顺序、dump=1、原目的地/UUID/密文不变、成功状态一致。 |
| ResumeRemotePhase | `TestResumeUsesPersistedDestination`、`TestResumeHonorsCancel`、`TestResumeRetriesTransientFailure`、`TestResumeFinalizesExistingRemoteCommit`：分别覆盖改绑、取消、failed/uploading 后续恢复和缺本地文件的补提交。 |
| Enqueue / BackupNow / uploadAndCommitRemote | `TestMetadataRollbackCannotOverwriteCommittedBackup`、`TestMigratedIdentityDoesNotCollide`、`TestUUIDLookupFailureHasNoRemoteWrites`：复用真实 SQLite 快照与冲突桶。 |
| runRemoteRetention / deleteRemoteBackup | `TestRetentionRejectsUnverifiedAnchor`、`TestDeleteCrashRecovery`、`TestRetentionFailedArtifactBudget30`、`TestRetentionKeepsActiveDownload`：锚点、删除崩溃、连续 30 次失败、下载租约。 |
| pruneLocalArtifacts / deleteRemoteBackup | `TestLocalAndRemoteRetentionIndependent`、`TestPruneRetainsFailedDeleteReferences`：10/1、1/10、count/day 及 unlink 错误。 |
| DeleteDestination / AssignDestination / BuildBackend | `TestAssignDeleteDestinationAtomic`、`TestDeleteDestinationDuringDump`、`TestDeletedDestinationHistoricalDownload`、`TestConcurrentDefaultBackendFactory`：barrier 与 -race 覆盖生命周期，不能只注入预建 fake。 |
| Store.Put / Delete / List | `TestMultipartCancelAbortsRealUploadID`、`TestMultipartSIGKILLRecovery`、`TestVersionedDeleteB2`、`TestListOver1000`：SDK 传输与真实 provider 两层，检查残留而不只读到对象。 |
| DiagnosticTest / CreateDestination / TestDestination | `TestDiagnosticDeleteDenied`、`TestDiagnosticReadAndCleanupBothFail`、`TestDestination422FixedMessage`：DELETE 权限、双错误、固定文本、无 secret 回传。 |
| Reconcile | `TestReconcileWithoutSQLiteUsesManifests`、`TestReconcileCategoriesExclusive`、`TestReconcileDiagnosticPrefixBoundaries`、`TestReconcileConcurrentCommit`：只读发现、互斥分类、正确 namespace、稳定报告。 |
| DownloadTask / GetTaskDownloadURL / 全部错误出口 | `TestDownloadRootConfinement`、`TestPresignLeaseProtectsRetention`、`TestStorageErrorsNeverLeakSecrets`：链接/竞态、签名 URL 生命周期、日志/DB/API 编码秘密矩阵。 |
| TestUploadVerifyMismatchDeletesRemote / TestUploadFailureRetainsArtifact / TestMinIOEndToEnd | 修正旧键与错误 hash；追加 mutation/负向对照，必须能检出禁用清理、禁用重试、相同大小错误内容、缺第二页及失效签名。 |

## 总体结论

**未通过。** 正常路径的提交顺序、强制完整性校验、随机 UUID 和默认工厂并发已有实质修复；恢复、不可变对象身份、删除状态与版本处理尚不足以满足 Phase 3 的“重启幂等收敛、删除绝不误删”和逐家验收要求。

最关键的 3 件事：

1. **先闭合协议 C 的恢复状态机**：固定 artifact/目的地快照，遵守取消，核对远端事实后补提交，暂时失败可继续；元数据库回滚不得覆盖原备份。
2. **落实协议 D 的副本治理**：合格锚点、deleting → 确认 → deleted、B2/version ID、独立本地保留、活动引用和失败预算必须共同成立。
3. **把验证变成能挡住回归的门禁**：修复当前假阳性测试，补崩溃/取消/权限故障及真实三桶矩阵；同时修复诊断清理错误吞失与秘密错误出口。
