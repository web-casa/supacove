// Landing-page copy. Every claim here restates something the docs already
// say (content/docs/*/index.mdx, quickstart.mdx, restore.mdx, …) — keep them in
// sync when the product changes. Section titles are split into phrases that
// must not break internally (CJK has no spaces to guide the line breaker).
export const homeCopy = {
  zh: {
    docs: "文档",
    eyebrow: "自托管 · PostgreSQL · age 加密",
    // {platform} is replaced by the rotating platform name (PLATFORMS in
    // components/hero.tsx); titleLabel is the same headline for screen readers.
    title: ["额外的第三方备份支持", "for {platform}."],
    titleLabel: "额外的第三方备份支持，for Supabase、Neon、Aiven、Prisma Postgres、TigerData、Miget、Railway、Render。",
    lede: "SupaCove 用 pg_dump 导出托管 PostgreSQL 的数据，流式加密后备份到你自己的 S3 兼容对象存储。失败明确可见，恢复独立于实例。",
    plansLink: "怎么备份 Supabase 数据库",
    plans: [
      ["免费套餐", "补上自动备份", "为 Supabase、Neon、Railway 的免费套餐按 Cron 计划自动备份，旧备份按保留策略清理。"],
      ["付费套餐", "多一份第三方备份", "在平台自带的备份之外，再留一份加密副本在你自己的对象存储里。"],
    ],
    primary: "快速开始",
    secondary: "先看怎么恢复",
    facts: [
      ["内置客户端", "PostgreSQL 14–18"],
      ["加密", "X25519 + ChaCha20-Poly1305"],
      ["存储", "S3 / R2 / B2 / MinIO"],
      ["许可", "AGPL-3.0"],
    ],
    stream: { plain: "明文", cipher: "密文", gate: "age" },
    flow: {
      source: "源库",
      also: "Aiven、Prisma Postgres、TigerData、Miget、Render 按标准 PostgreSQL 接入，尚未逐一实测",
      storage: "存储",
      stages: ["导出", "加密", "暂存", "上传"],
      stores: ["S3", "R2", "B2", "MinIO"],
    },
    crypto: {
      label: "加密",
      title: ["私钥只出现一次，", "由你离线保管。"],
      body: "备份在导出的同时用 age 流式加密。默认情况下实例内只保存公钥，解密用的私钥由你离线保管。",
      points: [
        ["内置 age 算法库", "X25519 + ChaCha20-Poly1305，无需外部 age CLI"],
        ["实例默认只存公钥", "实例内只保存公钥（recipient）。唯一的例外是开启恢复验证：那需要把私钥文件交给运行中的实例"],
        ["丢失私钥 = 备份不可恢复", "identity.txt 是解密所有备份的唯一私钥，请离线加密保存"],
      ],
      link: "初始化 age 加密身份",
      card: { offline: "离线保存", once: "私钥只出现这一次", recipient: "实例内只保存公钥" },
    },
    quickstart: {
      label: "快速开始",
      title: ["三条命令，", "跑起一台实例。"],
      body: "构建镜像、启动容器、领取一次性令牌。接着初始化 age 身份、注册数据库并做首次备份，最后验证一次恢复。",
      link: "按六步走完快速开始",
      term: {
        tab: "快速开始",
        comments: ["构建镜像", "启动", "初始化管理员"],
        next: ["初始化 age 身份", "注册数据库并备份", "验证恢复"],
        nextLabel: "接着",
      },
    },
    pipeline: {
      label: "流水线",
      title: ["一次备份，", "六个阶段。"],
      body: "导出、加密、提交任何一步失败，任务都会落为 failed。验证和通知各有自己的状态：验证失败不改写任务结果，通知重试耗尽会标记 dead。",
      optional: "可选",
      stages: [
        ["导出", "pg_dump --format=custom"],
        ["加密", "age 流式加密，X25519 + ChaCha20-Poly1305"],
        ["暂存", "本地原子提交：.inprogress → 最终名"],
        ["上传", "配置了目的地时写入远端对象存储，读回哈希校验"],
        ["验证", "在内嵌 PostgreSQL 里真实恢复一次"],
        ["通知", "outbox 投递 webhook，带重试"],
      ],
    },
    guarantees: {
      label: "保证",
      title: ["四条保证，", "不打折扣。"],
      items: [
        ["失败不伪装成功", "导出、加密、提交任何一步失败，任务都会落为 failed，绝不会标记 succeeded。"],
        [
          "重启可收敛",
          "停机中断的任务记为 interrupted，不产生假失败告警。满足续传条件时启动自动续传；不满足时保持可解释的终态，由下一次计划运行恢复备份链。",
        ],
        ["备份独立可恢复", "恢复只需要三样东西：密文文件、离线保存的 age 私钥、恢复套件脚本。"],
        ["秘密不出错误面", "密码与凭据在日志、API 错误、任务记录、指标四条出口都被脱敏。"],
      ],
    },
    features: {
      label: "功能",
      title: ["每个功能，", "都有一页文档。"],
      all: "查看全部文档",
      // Order matches FEATURES in components/features.tsx.
      items: [
        ["age 加密，私钥在你手里", "X25519 + ChaCha20-Poly1305；实例默认只保存公钥（recipient），私钥由你离线保管"],
        ["Cron 计划与新鲜度阈值", "标准五段或 @daily 别名，IANA 时区；超过阈值未成功即 EXPIRED"],
        ["Webhook 通知，至少一次", "三种事件经 outbox 投递，退避重试，耗尽后标记 dead"],
        ["心跳监控", "成功且新鲜的备份 ping 一次，失败 ping /fail；超时未 ping 由外部监控告警"],
        ["托管或自托管，一套流程", "Supabase、Neon、Railway 有专门的连接提示；Aiven、Prisma Postgres、TigerData、Miget、Render 按“自托管/其他”注册"],
        ["内嵌恢复验证", "每次成功备份后在内嵌 PostgreSQL 里恢复一次；默认关闭，开启需向实例提供私钥文件"],
      ],
      art: { manual: "仅手动", retry: "重试", platforms: ["Supabase", "Neon", "Railway", "自托管"],
        more: ["Aiven", "Prisma Postgres", "TigerData", "Miget", "Render"],
      },
    },
    restore: {
      title: ["实例没了，", "备份还在。"],
      body: "元数据库不是恢复的前提。manifest 自描述：服务器版本、表数、SHA-256、recipient 指纹都在里面。",
      parts: [
        ["*.dump.age", "密文，连同同名 manifest"],
        ["identity.txt", "离线 age 私钥"],
        ["restore.sh", "恢复套件"],
      ],
      result: "你的数据库",
      steps: ["校验密文 SHA-256", "拒绝非空目标库", "age 解密", "pg_restore", "manifest 有表数时核对"],
      link: "恢复与灾备",
    },
    faq: {
      label: "常见问题",
      title: ["先回答，", "再细说。"],
      // source/checked apply to the first answer only (a third-party policy).
      source: "Supabase 官方文档",
      checked: "核对于 2026-10-08",
      items: [
        ["Supabase 免费套餐有备份吗？", "Supabase 官方文档写明免费套餐的项目没有自动备份，并建议定期导出数据、保留异地副本。SupaCove 按 Cron 计划替你做这件事。"],
        ["和平台自带的备份有什么区别？", "平台的备份留在平台里。SupaCove 的副本在写出前就已加密，存进你自己的对象存储，私钥由你离线保管，恢复时不需要这台实例。"],
        ["备份包含 Supabase Storage 里的文件吗？", "不包含。备份是对 Postgres 数据库的一次完整 pg_dump：包含 auth 用户记录和 storage 对象元数据，不包含文件内容、Edge Functions 代码和项目设置。"],
        ["恢复需要什么？", "密文及其 manifest、离线保存的 age 私钥、恢复套件脚本，以及一个空的 PostgreSQL 数据库。Supabase 的备份要恢复到带有 Supabase 扩展的服务器上，步骤见 Supabase 指南。"],
        ["支持哪些平台？", "Supabase、Neon、Railway 有专门的连接提示。Aiven、Prisma Postgres、TigerData、Miget、Render 是标准 PostgreSQL，按“自托管/其他”注册，我们尚未逐一实测。CockroachDB 不支持。"],
      ],
    },
    index: { label: "目录", title: ["全部文档，", "按阅读顺序。"], guides: "指南" },
    closing: {
      label: "下一步",
      title: ["先恢复一次，", "再相信备份。"],
      body: "快速开始一共六步，最后一步就是用恢复套件把刚做的备份恢复到一个新库里。强烈建议做一次。",
    },
    footer: { license: "以 AGPL-3.0 许可发布", top: "回到顶部" },
  },
  en: {
    docs: "Docs",
    eyebrow: "Self-hosted · PostgreSQL · age encryption",
    title: ["An extra third-party backup", "for {platform}."],
    titleLabel: "An extra third-party backup for Supabase, Neon, Aiven, Prisma Postgres, TigerData, Miget, Railway and Render.",
    lede: "SupaCove runs pg_dump against your managed PostgreSQL, encrypts the stream and stores it in S3-compatible object storage you own. Failures are explicit, and a restore does not depend on the instance.",
    plansLink: "How to back up a Supabase database",
    plans: [
      ["Free plans", "Add automatic backups", "Scheduled backups for Supabase, Neon and Railway free-plan databases, with old ones pruned by your retention policy."],
      ["Paid plans", "Add a third-party copy", "Alongside the platform's own backups, keep an encrypted copy in object storage you own."],
    ],
    primary: "Quickstart",
    secondary: "See how restores work",
    facts: [
      ["Bundled clients", "PostgreSQL 14–18"],
      ["Cipher", "X25519 + ChaCha20-Poly1305"],
      ["Storage", "S3 / R2 / B2 / MinIO"],
      ["License", "AGPL-3.0"],
    ],
    stream: { plain: "plaintext", cipher: "ciphertext", gate: "age" },
    flow: {
      source: "Source",
      also: "Aiven, Prisma Postgres, TigerData, Miget and Render connect as standard PostgreSQL; not yet tested by us",
      storage: "Storage",
      stages: ["export", "encrypt", "stage", "upload"],
      stores: ["S3", "R2", "B2", "MinIO"],
    },
    crypto: {
      label: "Encryption",
      title: ["The private key appears once, ", "and you keep it offline."],
      body: "Backups are encrypted with age while they are being dumped. By default the instance keeps the public recipient only; the identity that decrypts them stays offline with you.",
      points: [
        ["Built-in age library", "X25519 + ChaCha20-Poly1305, with no external age CLI"],
        ["Recipient only, by default", "The instance keeps the public recipient. The one exception is restore verification, which hands the running instance an identity file"],
        ["Lose the identity, lose the backups", "identity.txt is the only key that can decrypt them. Store it offline, encrypted"],
      ],
      link: "Create the age identity",
      card: { offline: "keep offline", once: "The private key is printed exactly once", recipient: "The instance keeps the recipient only" },
    },
    quickstart: {
      label: "Quickstart",
      title: ["Three commands ", "to a running instance."],
      body: "Build the image, start the container, claim the one-time token. Then create the age identity, register a database and take the first backup, and finally prove the restore.",
      link: "Follow all six steps",
      term: {
        tab: "quickstart",
        comments: ["build the image", "run it", "initialize the admin account"],
        next: ["Create the age identity", "Register a database and back it up", "Prove the restore"],
        nextLabel: "Then",
      },
    },
    pipeline: {
      label: "Pipeline",
      title: ["One backup, ", "six stages."],
      body: "A failure in export, encryption or commit lands the job as failed. Verification and notification carry their own status: a failed verification leaves the job result alone, and exhausted deliveries are marked dead.",
      optional: "optional",
      stages: [
        ["Export", "pg_dump --format=custom"],
        ["Encrypt", "age stream encryption, X25519 + ChaCha20-Poly1305"],
        ["Stage", "atomic local commit: .inprogress → final name"],
        ["Upload", "with a destination configured: remote object-storage commit, read-back hash check"],
        ["Verify", "a real restore into embedded PostgreSQL"],
        ["Notify", "webhooks through an outbox, with retries"],
      ],
    },
    guarantees: {
      label: "Guarantees",
      title: ["Four guarantees, ", "no asterisks."],
      items: [
        [
          "Failures never look like success",
          "Any failure in export, encryption or commit lands the job as failed — never as succeeded.",
        ],
        [
          "Restarts converge",
          "Shutdown-interrupted jobs are recorded as interrupted, with no false failure alerts. When the resume conditions hold, startup re-uploads automatically; otherwise the job keeps an explainable terminal state and the next scheduled run resumes the chain.",
        ],
        [
          "Backups restore standalone",
          "Recovery needs exactly three things: the ciphertext, your offline age identity, and the recovery kit script.",
        ],
        [
          "Secrets never leak into errors",
          "Passwords and credentials are redacted across logs, API errors, task history and metrics.",
        ],
      ],
    },
    features: {
      label: "Features",
      title: ["Every feature, ", "one page of docs."],
      all: "Browse all docs",
      // Order matches FEATURES in components/features.tsx.
      items: [
        [
          "age encryption, keys stay with you",
          "X25519 + ChaCha20-Poly1305. By default the instance stores only the recipient; the identity stays offline with you",
        ],
        ["Cron schedules and freshness", "Five-field cron or @daily aliases, IANA time zones; past the threshold, protection is EXPIRED"],
        ["Webhooks, at least once", "Three events through an outbox, retried with backoff, marked dead when exhausted"],
        ["Dead-man switch", "A ping after each successful, fresh backup, /fail on failure; your external monitor alerts on silence"],
        ["Managed or self-hosted, one flow", "Supabase, Neon and Railway get dedicated connection hints; Aiven, Prisma Postgres, TigerData, Miget and Render register as self-hosted/other"],
        ["Embedded restore verification", "A real restore into embedded PostgreSQL after each successful backup; off by default, and enabling it gives the instance an identity file"],
      ],
      art: { manual: "manual only", retry: "retry", platforms: ["Supabase", "Neon", "Railway", "Self-hosted"],
        more: ["Aiven", "Prisma Postgres", "TigerData", "Miget", "Render"],
      },
    },
    restore: {
      title: ["Lose the instance, ", "keep the backups."],
      body: "The metadata database is not a prerequisite. The manifest describes itself: server version, table count, SHA-256 and recipient fingerprint.",
      parts: [
        ["*.dump.age", "ciphertext, with its manifest"],
        ["identity.txt", "offline age identity"],
        ["restore.sh", "recovery kit"],
      ],
      result: "your database",
      steps: ["verify ciphertext SHA-256", "refuse a non-empty target", "age decrypt", "pg_restore", "check the table count if the manifest has one"],
      link: "Restore & disaster recovery",
    },
    faq: {
      label: "FAQ",
      title: ["Short answers ", "first."],
      // source/checked apply to the first answer only (a third-party policy).
      source: "Supabase documentation",
      checked: "checked 2026-10-08",
      items: [
        ["Does the Supabase Free Plan include backups?", "Supabase's documentation states that Free Plan projects do not get automatic backups and recommends exporting data regularly and keeping off-site copies. SupaCove does that on a cron schedule."],
        ["How is this different from the platform's own backups?", "Platform backups stay on the platform. A SupaCove copy is encrypted before it is written, lands in object storage you own, and is decrypted with a key you keep offline. Restoring it does not need this instance."],
        ["Does a backup include files in Supabase Storage?", "No. A backup is one full pg_dump of the Postgres database: it includes auth user records and storage object metadata, but not file contents, Edge Function code or project settings."],
        ["What do I need to restore?", "The ciphertext with its manifest, your offline age identity, the recovery kit script, and an empty PostgreSQL database. A Supabase backup restores onto a server that has Supabase's extensions; the Supabase guide has the steps."],
        ["Which platforms are supported?", "Supabase, Neon and Railway get dedicated connection hints. Aiven, Prisma Postgres, TigerData, Miget and Render are standard PostgreSQL and register as self-hosted/other; we have not tested them ourselves yet. CockroachDB is not supported."],
      ],
    },
    index: { label: "Contents", title: ["Every page, ", "in reading order."], guides: "Guides" },
    closing: {
      label: "Next",
      title: ["Restore it once,", "then trust it."],
      body: "The quickstart has six steps, and the last one restores the backup you just took into a fresh database with the recovery kit. Do it once.",
    },
    footer: { license: "Released under AGPL-3.0", top: "Back to top" },
  },
} as const;

export type HomeCopy = (typeof homeCopy)[keyof typeof homeCopy];
