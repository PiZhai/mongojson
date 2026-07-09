# 工作场景模块级预备需求列表

本文档重新整理工作/开发场景的 100 个模块级需求。每一项都按“可独立成为一个功能模块或小工具”的粒度组织，不再把同一模块拆成过细的按钮、字段或子能力。

筛选原则：

- 优先保留每天或每周会用、能本地处理敏感数据、能从粘贴内容直接跳转的工具。
- 如果需求必须结合代码、配置、命令输出和项目上下文判断根因，则标记为 `Agent入口`，不做孤立页面。
- 如果功能已经有成熟大型平台，当前项目只保留轻量、本地、粘贴优先的切面。
- 重合需求做合并，例如日志、报错、构建失败、端口问题统一进入“报错诊断 Agent 入口”系列；JWT、Base64、Hash、UUID、时间转换统一进入“开发编码工具箱”系列。

## 样本来源索引

| 标签 | 来源与信号 |
|---|---|
| RW01 | Reddit API 工具讨论：Postman/Insomnia 云化、臃肿、本地 Git 化诉求。[local-first API client](https://www.reddit.com/r/webdev/comments/1qyi3wz/rip_postman_free_tier_heres_an_opensource/)、[API client in Tauri](https://www.reddit.com/r/webdev/comments/1rtjwdj/showoff_saturday_i_built_an_open_source_api/) |
| RW02 | Reddit API 重复测试流：同一验证流程要在 API、DB、复制 OTP 间来回切换。[remove a few clicks](https://www.reddit.com/r/webdev/comments/1sov4kg/i_spent_a_year_building_a_devtool_just_to_remove/) |
| RW03 | Postman 2025 State of API：API 工作占用大量开发时间，测试、开发、文档是高频任务。[report](https://www.postman.com/state-of-api/2025/) |
| RW04 | Stack Overflow 2024：技术债、复杂技术栈、不可靠工具是主要开发摩擦。[survey](https://survey.stackoverflow.co/2024/professional-developers/) |
| RW05 | Atlassian/DX DevEx：开发者每周大量时间损失在低效、上下文切换和查找信息上。[Atlassian](https://www.atlassian.com/blog/development/developer-experience-report-2024)、[DX](https://getdx.com/report/state-of-developer-experience-report/) |
| RW06 | Reddit TypeScript/Zod/OpenAPI：运行时 Schema、类型生成、前后端契约同步长期有争议。[Zod source of truth](https://www.reddit.com/r/typescript/comments/10f8kah/is_using_zod_as_the_primary_source_of_truth_for/)、[API types](https://www.reddit.com/r/typescript/comments/1apjike/how_do_you_generate_and_manage_your_api_types/) |
| RW07 | Reddit DevOps/config：环境差异、IaC 分叉、配置漂移造成难排查问题。[IaC drift](https://www.reddit.com/r/devops/comments/1bk1nts/should_you_use_the_same_iac_code_to_deploy_to/) |
| RW08 | Reddit selfhosted dashboard：仪表盘容易过度噪音，真正需要的是行动信号。[dashboard noise](https://www.reddit.com/r/selfhosted/comments/1r1wjch/i_used_to_think_dashboards_were_dumb/) |
| RW09 | Reddit DevOps 碎片化：工具链越来越多但排障仍疲惫。[fragmented DevOps](https://www.reddit.com/r/devops/comments/1i538r1/why_is_devops_still_such_a_fragmented_exhausting/) |
| RW10 | Stack Overflow 2025：AI 工具结果需要验证，适合做证据包和 Agent 入口，而不是无证据结论。[survey](https://survey.stackoverflow.co/2025) |
| RW11 | Reddit dashboard/inventory：用户需要服务、机器、入口和说明的轻量目录。[dashboard descriptions](https://www.reddit.com/r/selfhosted/comments/18xgcsu/my_dashboard_now_with_descriptions/) |
| RW12 | Reddit local dev/CI：本地资源、Docker、环境准备仍是开发测试基础痛点。[CI/local testing](https://www.reddit.com/r/devops/comments/jo8jy4/developers_testing_things_in_a_real_cicd_pipeline/) |
| RW13 | Reddit web complexity：Web 开发复杂度上升，轻量工具有价值。[webdev complicated](https://www.reddit.com/r/webdev/comments/x5sra9/web_development_is_so_complicated/) |
| RW14 | MongoDB 复杂聚合讨论与 Compass explain 能力：pipeline 可视化、性能摘要有明确价值。[Compass explain](https://www.mongodb.com/docs/compass/agg-pipeline-builder/view-pipeline-explain-plan/) |
| RW15 | ITPro productivity drains：调试、文档、工具集成失败是开发者生产力损耗点。[article](https://www.itpro.com/software/development/clunky-tech-is-costing-developers-20-working-days-a-year-these-are-the-leading-productivity-drains-impacting-teams) |
| RW16 | Hacker News API 客户端讨论：Bruno 的 offline-first、Git 协作和 Insomnia 云化迁移争议强化了轻量本地 API 工具需求。[HN Bruno](https://news.ycombinator.com/item?id=39653718) |
| RW17 | GitHub Bruno 讨论：用户强调易安装、易用、VCS 分享、只保留需要的功能。[Bruno testimonials](https://github.com/usebruno/bruno/discussions/343) |
| RW18 | Ministry of Testing 社区：离线 API 调试、无需账号、本地请求、mock/OpenAPI 支持是测试人员的实际诉求。[offline API testing](https://club.ministryoftesting.com/t/tools-for-offline-api-testing-and-debugging-postman-alternatives/85717) |
| RW19 | GitHub Insomnia 讨论：用户不希望更新后被账号/同步策略锁住本地数据。[Insomnia account/local data](https://github.com/Kong/insomnia/discussions/6590) |
| RW20 | Hacker News JSON Schema/OpenAPI 讨论：JSON Schema、Zod、OpenAPI 的边界和互转复杂度带来长期工具需求。[Ask HN JSON Schema](https://news.ycombinator.com/item?id=34587360)、[OpenAPI ecosystem](https://news.ycombinator.com/item?id=41612002) |
| RW21 | Hacker News Zod 讨论：Schema 到 JSON Schema、preprocess/refine 等能力无法完全互通，支持 Schema 调试与转换需求。[Zod 4](https://news.ycombinator.com/item?id=44030850) |
| RW22 | GitHub/OpenAPI 工具生态：OpenAPI 到 Zod、类型安全客户端和 runtime validation 的生成器持续出现。[openapi-zod-client](https://github.com/astahmer/openapi-zod-client)、[zod-openapi topics](https://github.com/topics/zod-openapi) |
| RW23 | Speakeasy OpenAPI 文档：Zod 到 OpenAPI、SDK 生成等说明 contract-first 与类型生成是成熟工作流。[Zod OpenAPI](https://www.speakeasy.com/openapi/frameworks/zod) |
| RW24 | Software Engineering Stack Exchange：环境变量集中读取、启动时失败、配置默认值等体现配置体检需求。[env var design](https://softwareengineering.stackexchange.com/questions/433636/should-we-directly-read-environment-variables-when-where-we-need-them) |
| RW25 | Open Source Stack Exchange：不同开发环境中硬编码路径、配置与环境变量管理是长期工程问题。[dev environments](https://opensource.stackexchange.com/questions/1497/dealing-with-different-development-environments) |
| RW26 | JetBrains Developer Ecosystem：大型开发者生态报告补充 IDE、语言、AI 与开发工具使用背景。[JetBrains 2024](https://www.jetbrains.com/lp/devecosystem-2024/) |
| RW27 | JetBrains CodeCanvas：configuration drift 会随包、系统工具、环境变量偏离标准设置而累积。[configuration drift](https://blog.jetbrains.com/codecanvas/2025/08/configuration-drift-the-pitfall-of-local-machines/) |
| RW28 | arXiv 配置差异研究：容器、自动化、配置管理计划等是缓解 dev/prod 差异的策略。[configuration differences](https://arxiv.org/html/2505.09392v1) |
| RW29 | DEV Community 配置漂移文章：CI 很少验证配置，缺失变量、明文 secret、旧 key 会导致生产问题。[CI config drift](https://dev.to/jckilous/why-we-started-failing-ci-on-configuration-drift-11f4) |
| RW30 | Env Sentinel/NopAccelerate 配置漂移案例：本地可用、生产失败常来自环境变量、依赖版本和配置不一致。[Env Sentinel](https://www.envsentinel.dev/blog/why-it-works-on-my-machine-configuration-drift)、[nopAccelerate](https://www.nopaccelerate.com/configuration-drift-dev-vs-production-guide/) |
| RW31 | Hacker News 开发者不满讨论：服务启动、技术债、多服务依赖导致大量时间浪费。[SO survey Reddit/HN discussion](https://www.reddit.com/r/programming/comments/1flp3xt/stack_overflow_survey_80_of_developers_are_unhappy/) |
| RW32 | Stack Overflow 文档讨论：缺失、碎片化和不一致文档是生产力障碍，支持 Runbook/文档检查需求。[documentation toil](https://stackoverflow.blog/2024/12/19/developers-hate-documentation-ai-generated-toil-work/) |
| RW33 | ITPro 文档生产力报道：搜索信息、缺少文档、工具集成失败造成显著开发时间损失。[documentation focus](https://www.itpro.com/software/development/if-software-development-were-an-f1-race-these-inefficiencies-are-the-pit-stops-that-eat-into-lap-time-why-developers-need-to-sharpen-their-focus-on-documentation) |
| RW34 | Hacker News 个人生产力与单文件记录：轻量、低结构的记录系统能减少维护负担。[single txt productivity](https://news.ycombinator.com/item?id=22276184)、[productivity tools](https://news.ycombinator.com/item?id=35853576) |
| RW35 | Home/selfhosted 与服务目录讨论：轻量服务目录、说明、状态和入口比复杂仪表盘更实用。[infrastructure dashboard](https://www.reddit.com/r/selfhosted/comments/efg455/looking_for_an_infrastructure_visualization/) |
| RW36 | GitHub Octoverse：AI、agents、typed languages 正在改变开发工作流，强化类型、Schema、Agent 证据入口的必要性。[Octoverse](https://octoverse.github.com/)、[GitHub blog](https://github.blog/news-insights/octoverse/octoverse-a-new-developer-joins-github-every-second-as-ai-leads-typescript-to-1/) |
| RW37 | State of JavaScript：构建步骤和 TypeScript/类型痛点是现代前端复杂度来源。[State of JS usage](https://2024.stateofjs.com/en-US/usage/)、[analysis](https://patrickbrosset.com/articles/2024-12-16-my-very-short-and-incomplete-analysis-of-the-state-of-js-2024-survey-results/) |
| RW38 | Awesome API Clients/Product Hunt/API 工具清单：API client、offline devtools、JWT/hash/cron/JSON 工具类产品持续出现，说明需求稳定但竞争充分。[awesome-api-clients](https://github.com/stepci/awesome-api-clients)、[Wring](https://www.producthunt.com/products/wring) |
| RW39 | APIs You Won't Hate/Sematext API 客户端比较：HTTPie、Bruno、Hoppscotch、Thunder Client 等定位差异说明“轻量本地切面”更可行。[API clients](https://apisyouwonthate.com/blog/http-clients-alternatives-to-postman/)、[Postman alternatives](https://sematext.com/blog/best-8-postman-alternatives-reviewed-and-compared/) |
| RW40 | Bruno/Autonoma/Testsprite 比较文章：API 工具市场的核心分歧集中在本地、Git、云同步、测试自动化和团队协作边界。[Bruno vs Postman](https://www.usebruno.com/compare/bruno-vs-postman)、[alternatives](https://getautonoma.com/blog/postman-alternatives)、[best tools](https://www.testsprite.com/use-cases/en/the-best-postman-tools) |
| RW41 | Product Hunt/API 变更案例：第三方 API 会变化，API contract、请求样例和变更检测有长期价值。[Product Hunt API](https://www.producthunt.com/topics/api-1)、[API changes](https://norahsakal.com/blog/product-hunt-api-changes/) |
| RW42 | DEV/社区 Schema 工具生态：Zod、Valibot、Runtypes、Arktype、Typia、Effect Schema 等说明 schema validation 已是成熟痛点。[schema libraries](https://dev.to/dzakh/javascript-schema-library-from-the-future-5420) |
| RW43 | State of Frontend：Zod/TypeScript 推断解决 validation 与 type safety 同步痛点。[State of Frontend](https://tsh.io/state-of-frontend) |
| RW44 | Product Hunt/offline devtools：离线菜单栏开发工具覆盖 JWT、hash、regex、JSON、Base64、timestamp、cron、colors、UUID、diff、env secrets，验证开发编码工具箱方向。[Wring](https://www.producthunt.com/products/wring) |
| RW45 | YouTube/内容社区 API 工具对比：轻量、免费、替代 Postman 的内容持续出现，说明需求广但差异化应落在本地隐私和粘贴流转。[Postman alternative video](https://www.youtube.com/watch?v=MimFVcTQOB8&vl=en)、[HTTP clients](https://www.youtube.com/watch?v=m7mZyuWN-oE) |

## 标记说明

| 标记 | 含义 |
|---|---|
| 值得做 | 独立工具比 Agent 更快、更稳定、更高频。 |
| Agent入口 | 工具负责识别、收集上下文、生成证据包，根因判断交给 Agent。 |
| 后续观察 | 有需求信号，但频率、边界或差异化还需验证。 |
| 暂不建议 | 成本高、重复成熟平台、或偏离当前项目定位。 |

## 筛选方法与结论

筛选维度：

- 高频性：是否属于每天/每周会碰到的开发动作，而不是偶发大型工程。
- 本地敏感性：是否涉及 token、请求、日志、真实数据、配置等不适合上传外部网站的内容。
- 结构化程度：输入输出是否明确，能否做成稳定工具，而不是只能靠人判断。
- Agent 适配度：是否必须读项目代码、运行命令、看 Git diff 或结合环境才能判断。
- 与现有项目连续性：是否能复用智能粘贴、JSON/Mongo、Diff、Schema、MemoDocs 的能力。
- 实现成本与平台化风险：是否会膨胀成 Postman、DevOps 平台、数据库客户端或通用 AI Chat。

筛选后的优先队列：

| 优先级 | 模块 ID | 结论 |
|---|---|---|
| P0 核心入口 | W001, W002, W003, W004, W006, W009 | 最贴合“粘贴即进入工具、本地处理敏感数据”的产品主线，优先级最高。 |
| P0 核心工具 | W011, W012, W013, W014, W015, W017, W018 | API 请求/Mock/Contract Diff 是互联网样本最强的一组需求，但必须保持轻量本地，不做完整 Postman。 |
| P0 数据结构 | W026, W029, W030, W031, W032, W033, W034, W038 | JSON/Schema/Type/Mock/脱敏和现有能力连续，结构化程度高，Agent 替代性低。 |
| P1 Mongo 深化 | W041, W042, W043, W044, W045, W046, W047 | 继续做深当前 MongoDB JSON 工具，比另开大模块更划算。 |
| P1 配置与环境 | W051, W052, W053, W055, W056, W060 | 配置对比和服务目录适合工具化；根因判断部分保留 Agent 入口。 |
| P1 Agent 入口 | W061, W062, W063, W064, W065, W066, W068, W070, W071 | 报错/日志/构建/测试/端口问题不做孤立诊断平台，做证据包和上下文收集。 |
| P1 文档与知识 | W072, W073, W074, W075, W077, W078, W079 | 文档、Runbook、PR、评审更适合 Agent 生成，但 MemoDocs 可承接结果。 |
| P1 编码工具箱 | W081, W082, W083, W084 | 来源广、成本低、隐私价值高，可合并成一个模块，不拆散。 |
| P2 观察 | W023, W025, W035, W049, W085, W086, W087, W088, W089, W090, W093, W097 | 有使用场景，但要等核心入口和 API/Schema/Mongo 完成后再评估。 |
| 暂不建议 | W050, W080, W099, W100 | 完整数据库客户端、团队知识库、通用 AI Chat、完整 DevOps 平台都会偏离当前定位。 |

当前推荐落地顺序：

1. 剪贴板收纳箱 + 智能粘贴路由中心。
2. 轻量 API 请求与 Mock 工作台。
3. Schema / Type / Mock Data / 脱敏工作台。
4. Mongo Pipeline 工作台深化。
5. 开发编码工具箱。
6. 报错诊断 Agent 入口与 Runbook 生成。

## 100 个模块级需求

| ID | 模块/小工具 | 主要痛点 | 核心功能范围 | 来源标签 | 建议 |
|---|---|---|---|---|---|
| W001 | 智能粘贴路由中心 | 复制内容后不知道该去哪个工具处理 | 识别 JSON、curl、JWT、日志、SQL、env、Markdown、Mongo Shell，并跳转到最合适工具 | RW04/RW05 | 值得做 |
| W002 | 开发剪贴板收纳箱 | JSON、报错、curl、token、命令片段被覆盖后找不到 | 本地历史、类型识别、脱敏预览、收藏、发送到工具 | RW05/RW13 | 值得做 |
| W003 | 混合文本结构抽取器 | 日志、聊天、网页中夹着 JSON、URL、错误栈和命令 | 从大段文本抽取结构化片段，并生成可继续处理的卡片 | RW05/RW15 | 值得做 |
| W004 | 敏感内容脱敏工作台 | token、cookie、邮箱、手机号、内网地址不适合外发 | 本地扫描、分级风险、格式保留脱敏、复制前确认 | RW10/RW15 | 值得做 |
| W005 | 工作区会话草稿箱 | 临时分析过程结束后输入、输出和结论丢失 | 保存工具状态、输入输出、备注、来源链接，可恢复会话 | RW05 | 值得做 |
| W006 | 多工具流水线编排器 | 同一份数据要在格式化、Diff、Schema、Mock 间来回复制 | 结果可一键发送到下一个工具，保留处理链路 | RW05/RW13 | 值得做 |
| W007 | 轻量工具首页与最近工作 | 工具多后入口选择成本上升 | 最近使用、收藏、粘贴推荐、待处理草稿和状态提醒 | RW08/RW11 | 值得做 |
| W008 | 本地样例与模板库 | 常用输入样例散落在文档和聊天记录 | 保存 API、JSON、Mongo、正则、配置模板，支持参数化复用 | RW05/RW11 | 值得做 |
| W009 | 工具结果 Markdown 报告器 | 分析结果难发给同事或贴到 issue | 将输入摘要、处理结果、风险、下一步合成 Markdown | RW05/RW15 | 值得做 |
| W010 | 本地优先数据策略面板 | 不清楚哪些工具会保存或上传敏感内容 | 显示每个模块的数据存储、清理、导出、隐私说明 | RW10 | 值得做 |
| W011 | 轻量 API 请求工作台 | Postman/Insomnia 太重、云化、协作负担大 | curl/OpenAPI 导入、请求执行、headers/body/auth 编辑、响应查看 | RW01/RW03 | 值得做 |
| W012 | curl 解析与请求重建器 | 从终端/文档复制 curl 后手工拆解容易错 | 解析 method、URL、query、headers、cookie、body，并可重组 curl | RW01/RW03 | 值得做 |
| W013 | API 响应检查器 | 接口返回 JSON 后还要切到别的工具格式化和 Diff | 响应格式化、路径搜索、Schema 摘要、状态码与业务码检查 | RW03 | 值得做 |
| W014 | API 响应 Diff 工作台 | 两次接口响应变化难判断影响 | 语义 Diff、字段变化、数组策略、兼容性风险提示 | RW03/RW06 | 值得做 |
| W015 | API Mock 响应生成器 | 前端等后端接口时 mock 搭建成本高 | 从样例响应生成 mock、延迟、状态码、错误响应和随机字段 | RW03 | 值得做 |
| W016 | OpenAPI 导入整理器 | OpenAPI 文件大且请求示例不好直接使用 | 按 tag/path 生成请求集合、示例 body、参数表和缺失项提示 | RW03/RW06 | 值得做 |
| W017 | API Contract Diff 工具 | OpenAPI、请求集合、前端类型经常漂移 | 比较 spec、collection、样例响应，标记破坏性变更 | RW03/RW06 | 值得做 |
| W018 | API 环境变量工作台 | local/dev/test/prod 变量散落，切环境容易错 | 变量集、引用预览、缺失检查、敏感值遮罩、导入导出 | RW01/RW07 | 值得做 |
| W019 | API 认证辅助工具 | bearer、cookie、HMAC、签名参数处理繁琐且敏感 | 本地 token 注入、签名预览、过期提示、敏感字段保护 | RW01/RW03 | 值得做 |
| W020 | API 调试证据包 Agent 入口 | 请求失败原因常跨网络、CORS、代理、后端和配置 | 收集请求、响应、错误、环境变量和候选检查命令交给 Agent | RW05/RW10/RW15 | Agent入口 |
| W021 | API 调用代码生成器 | 联调后需要把请求转成前端/后端代码 | 生成 fetch、axios、React Query、Go http、Node undici 片段 | RW03/RW06 | 值得做 |
| W022 | API 文档片段生成器 | 文档缺少真实示例和字段说明 | 从请求、响应、Schema 生成 Markdown 文档片段 | RW03/RW15 | 值得做 |
| W023 | API 批量冒烟测试器 | 不想引入完整测试平台但想快速验证一组接口 | 请求集合顺序执行、基础断言、耗时统计、失败摘要 | RW03/RW12 | 后续观察 |
| W024 | API 错误响应规范检查器 | HTTP 状态、业务 code、message、error 格式不统一 | 识别响应包装模式，比较错误结构一致性 | RW03/RW06 | 值得做 |
| W025 | API 用例录制回放器 | 手动重复测试登录、验证、支付等流程费时 | 保存多步请求、变量提取、前后步骤关联、回放报告 | RW02/RW03 | 后续观察 |
| W026 | JSON 修复与格式化工作台 | JSON 不合法时错误信息难懂 | 修复、格式化、压缩、定位错误、保留原文对照 | RW04 | 值得做 |
| W027 | JSON 树与路径浏览器 | 大 JSON 找字段和复制路径困难 | 树视图、路径复制、搜索、折叠、类型和大小统计 | RW04/RW05 | 值得做 |
| W028 | JSON/NDJSON 表格化工具 | 日志、导出数据和接口数组需要表格分析 | 扁平化路径、字段缺失率、类型筛选、CSV 导出 | RW04 | 值得做 |
| W029 | 语义 JSON Diff 工具 | 文本 Diff 对结构化数据噪音太大 | 按路径比较新增、删除、类型变化、值变化和兼容性 | RW04/RW06 | 值得做 |
| W030 | JSON Schema 推断器 | 样本数据缺少结构说明 | 从多个样本推断 JSON Schema、可选字段、nullable、mixed type | RW06 | 值得做 |
| W031 | Schema 到类型生成器 | 前后端类型和运行时校验不同步 | 从 Schema 生成 TypeScript、Zod、Go struct、OpenAPI schema | RW06 | 值得做 |
| W032 | Mock Data 生成器 | 测试数据手写慢且不稳定 | 从 Schema/样本生成多条 mock 数据，支持 seed、规则和脱敏 | RW06 | 值得做 |
| W033 | 数据脱敏与伪造器 | 真实样本不能直接发给同事或 AI | 字段识别、保留格式替换、批量生成、脱敏报告 | RW10/RW15 | 值得做 |
| W034 | Schema 漂移分析器 | 多批数据字段和类型悄悄变化 | 比较样本集合，输出字段新增、缺失、类型漂移和风险 | RW06 | 值得做 |
| W035 | JSONPath/JQ 辅助器 | 临时提取字段时表达式难写 | 点击字段生成 JSONPath/JQ，支持过滤预览和结果导出 | RW04 | 后续观察 |
| W036 | 结构化格式转换器 | JSON/YAML/TOML/CSV 之间转换要打开多个网站 | 本地转换、错误定位、字段映射、转换损失提示 | RW04 | 值得做 |
| W037 | 大 JSON 性能采样器 | 超大 JSON 打开卡顿但又要看结构 | 采样、大小统计、前 N 条分析、字段摘要、截断策略 | RW04/RW05 | 值得做 |
| W038 | 数据质量体检器 | 样本数据中空值、异常值、重复值难发现 | 缺失率、唯一值、范围、格式异常、重复记录扫描 | RW04 | 值得做 |
| W039 | JSON 到文档说明生成器 | 字段说明和示例文档手写重复 | 从样本生成字段表、示例、约束草稿和备注占位 | RW03/RW15 | Agent入口 |
| W040 | 数据对齐检查器 | 前端、后端、测试、文档使用的数据结构不一致 | 对比样例、Schema、类型文件和接口响应生成差异报告 | RW06/RW15 | Agent入口 |
| W041 | MongoDB JSON/Shell 格式化器 | Extended JSON、Shell notation、普通 JSON 混用 | 自动识别、格式化、Canonical 输出、还原和复制模式 | RW14 | 值得做 |
| W042 | Mongo 查询风险检查器 | update/delete/aggregate 风险不直观 | 识别无条件更新、危险 operator、multi、集合和字段影响 | RW14 | 值得做 |
| W043 | Mongo Pipeline 工作台 | aggregation pipeline 长且难理解 | 按 stage 展示字段流向、输入输出结构、风险和摘要 | RW14 | 值得做 |
| W044 | Pipeline 性能风险提示器 | `$lookup`、`$unwind`、`$group` 可能导致爆炸和慢查询 | 检查 stage 顺序、潜在大数组、索引需求和内存风险 | RW14 | 值得做 |
| W045 | Mongo 文档 Schema 分析器 | 集合样本字段漂移影响查询和前端 | 多文档 Schema、缺失率、类型分布、样例行预览 | RW14/RW06 | 值得做 |
| W046 | Mongo 更新影响摘要器 | `$set/$unset/$inc` 影响字段难快速确认 | 提取 filter、operator、字段路径、潜在影响和可读摘要 | RW14 | 值得做 |
| W047 | Mongo 代码导出器 | Shell 语句要转成不同语言驱动代码 | 导出 Node、Go、Java、Python 片段，保留参数和注释 | RW14 | 值得做 |
| W048 | Mongo explain 准备清单 | 真正性能判断需要数据库 explain，但语句可先预检 | 根据 query/pipeline 生成 explain checklist 和索引候选 | RW14 | Agent入口 |
| W049 | Mongo 语句测试样本生成器 | 查询、更新、pipeline 缺少可复现样例 | 从字段和条件生成小集合样本、预期结果和边界数据 | RW14/RW06 | 后续观察 |
| W050 | 完整数据库客户端 | 连接生产库、账号权限、在线执行查询风险高 | 当前项目不做完整 DB 客户端，只做离线分析和样本工具 | RW14 | 暂不建议 |
| W051 | 环境变量矩阵对比器 | local/dev/prod/CI 变量缺失或命名不一致 | 对比 env 文件、示例文件和文档，标记缺失、多余、冲突 | RW07 | 值得做 |
| W052 | 配置文件结构化 Diff 工具 | YAML、JSON、TOML、Compose 文本 Diff 噪音大 | 按 key/path 比较，识别重复 key、类型变化、默认值差异 | RW07 | 值得做 |
| W053 | Docker Compose 解析器 | services、ports、volumes、env、depends_on 难一眼看清 | 可视化服务关系、端口映射、卷、环境变量和健康检查 | RW12/RW07 | 值得做 |
| W054 | Nginx/代理配置检查器 | 反代路径、proxy_pass、rewrite 配置易错 | 解析 server/location、路径匹配、上游地址和冲突提示 | RW07/RW15 | Agent入口 |
| W055 | 端口与 URL 一致性检查器 | 前端、后端、代理、文档端口不一致 | 抽取配置中的 host/port/url，生成一致性矩阵和风险提示 | RW07/RW12 | Agent入口 |
| W056 | 本地开发环境快照器 | Node、Go、Java、Docker、数据库版本漂移 | 采集版本、端口、服务状态和关键 env，生成可对比快照 | RW12/RW05 | Agent入口 |
| W057 | CI 配置体检器 | 本地命令与 CI workflow 不一致 | 解析 package scripts、Makefile、GitHub Actions，生成命令矩阵 | RW12/RW15 | Agent入口 |
| W058 | 依赖升级风险摘要器 | lockfile diff 难看，升级影响不清 | 标出主版本升级、直接/间接依赖、已知风险包和测试建议 | RW04/RW15 | Agent入口 |
| W059 | IaC/配置漂移检查入口 | dev/staging/prod 配置分叉导致隐蔽问题 | 收集环境差异、变量覆盖、模块版本，交给 Agent 诊断 | RW07/RW09 | Agent入口 |
| W060 | 服务目录与本地链接面板 | 本地服务、文档、监控、管理入口散落 | 保存服务名、URL、端口、用途、健康检查和备注 | RW08/RW11 | 值得做 |
| W061 | 报错诊断 Agent 入口 | 单独看报错无法知道根因，必须结合代码和环境 | 提取 stack、路径、行号、端口、模块、命令，生成 Agent 诊断包 | RW10/RW15 | Agent入口 |
| W062 | 构建失败摘要器 | 构建日志长，真正失败任务被淹没 | 提取 failed task、exit code、包名、错误块、相关文件 | RW04/RW15 | Agent入口 |
| W063 | 测试失败摘要器 | 多个测试失败时首个原因和聚类难看 | 解析测试名、断言、expected/received、堆栈和重复失败 | RW15 | Agent入口 |
| W064 | TypeScript 错误读解器 | TS 错误链长且类型路径复杂 | 提取 error code、文件、类型链、候选修复方向和证据 | RW06/RW15 | Agent入口 |
| W065 | 前端运行时错误入口 | 浏览器 console 错误需要关联源码、路由和组件 | 解析 stack、URL、source map 线索、组件名，交给 Agent 定位 | RW10/RW15 | Agent入口 |
| W066 | 后端堆栈摘要入口 | Java/Go/Node 堆栈很长，root cause 被包裹 | 抽取 caused by、项目帧、goroutine/thread、配置线索 | RW15 | Agent入口 |
| W067 | 日志清洗与聚类工具 | 日志刷屏，重复错误和噪声太多 | 去 ANSI、按 fingerprint 聚类、时间线、level/service/URL 抽取 | RW09/RW15 | 值得做 |
| W068 | 端口占用诊断入口 | 端口被占用或保留时普通错误信息不够 | 收集监听进程、保留端口、配置端口、建议检查命令 | RW12/RW15 | Agent入口 |
| W069 | 网络/API 失败分类器 | ECONNREFUSED、CORS、TLS、DNS、proxy 混在一起 | 根据错误文本分类并生成下一步验证命令 | RW15 | Agent入口 |
| W070 | 诊断证据报告器 | AI 或人工排障容易给猜测缺证据 | 强制输出证据行、文件、命令、假设、不确定性和下一步 | RW10/RW15 | Agent入口 |
| W071 | Runbook 生成器 | 排障过程结束后没有可复用文档 | 从对话/命令/结论生成问题、环境、步骤、结果、复盘 | RW05/RW15 | Agent入口 |
| W072 | MemoDocs 工程模板库 | 备忘录缺少工程化模板 | 提供排障、API、发布、会议、学习、配置记录模板 | RW05 | 值得做 |
| W073 | 文档新旧检查入口 | README、部署文档、API 文档容易过期 | 对比代码路由、配置、脚本和文档内容，生成差异清单 | RW15 | Agent入口 |
| W074 | Markdown 结构整理器 | 长文档标题、链接、代码块容易混乱 | 标题层级、坏链接、目录、代码块语言、敏感信息检查 | RW15 | 值得做 |
| W075 | API 文档一致性助手 | 接口文档、OpenAPI、真实响应不一致 | 把 spec、样例、请求集合和说明合并检查 | RW03/RW06 | Agent入口 |
| W076 | 会议/需求纪要整理器 | 讨论后行动项、风险、决策分散 | 从文本生成决策、待办、疑问、风险和跟进清单 | RW05 | Agent入口 |
| W077 | PR/Commit 描述生成器 | 提交说明、PR 描述重复手写 | 基于 diff、任务、测试输出生成结构化描述草稿 | RW05/RW15 | Agent入口 |
| W078 | 技术方案检查器 | 方案常缺范围、验收、风险、迁移和回滚 | 检查方案完整性并生成缺口列表 | RW04/RW15 | Agent入口 |
| W079 | 代码评审准备器 | Review 前不知道哪些文件和风险重点 | 从 diff 生成风险点、测试建议、兼容性检查清单 | RW04/RW15 | Agent入口 |
| W080 | 完整团队知识库 | 评论、权限、审批、多人协同会把项目做重 | 当前不做 Wiki/Confluence 替代，只做轻量文档工具 | RW05 | 暂不建议 |
| W081 | 开发编码工具箱 | 编码、解码、hash、UUID、时间转换频繁打开外部网站 | 本地 URL/Base64/HTML/JWT/Hash/HMAC/UUID/Timestamp/Cron | RW05 | 值得做 |
| W082 | JWT 本地检查器 | JWT 常含敏感信息，不适合贴到公网工具 | header/payload 解码、过期时间、alg、issuer、脱敏复制 | RW10 | 值得做 |
| W083 | Hash/HMAC 计算器 | 签名、校验、摘要临时计算分散 | 支持 SHA、MD5、HMAC、大小写、分隔符、文件 hash | RW05 | 值得做 |
| W084 | Cron/时间戳/时区工作台 | Cron、UTC/local、DST 容易错 | Cron 解释、未来 N 次、Unix/ISO 转换、时区对比 | RW13 | 值得做 |
| W085 | Regex/文本提取工作台 | 正则测试、替换和日志提取需要来回试 | 样本测试、捕获组、替换预览、多行模式、常用模板 | RW05 | 后续观察 |
| W086 | SQL 文本工具箱 | SQL 格式化、参数占位、Explain 前检查不想连库 | 格式化、参数提取、危险语句提示、字段/表名摘要 | RW15 | 后续观察 |
| W087 | 文件元数据与校验工具 | 文件类型、hash、MIME、大小、EXIF 临时检查 | 本地读取元数据、hash、EXIF 脱敏提示、复制摘要 | RW10 | 后续观察 |
| W088 | 证书/域名基础检查器 | TLS 过期、DNS、证书链问题发现晚 | 域名证书、到期时间、DNS 基础记录、HTTPS 检查入口 | RW09 | 后续观察 |
| W089 | 颜色与设计 Token 工具 | 前端样式调试需要颜色转换和 token 对齐 | HEX/RGB/HSL 转换、对比度、token 命名、色板导出 | RW13 | 后续观察 |
| W090 | 图片压缩与尺寸检查器 | 上传前需要压缩、查尺寸和格式 | 本地压缩、尺寸、格式转换、透明背景检查 | RW05 | 后续观察 |
| W091 | 发布前检查清单 | 发版前步骤多且易漏 | 版本、build、lint、迁移、环境变量、回滚、备份 checklist | RW12/RW15 | Agent入口 |
| W092 | 部署状态摘要入口 | 服务状态、日志、健康检查分散 | 收集 health、docker ps、日志 tail、版本信息并生成摘要 | RW09/RW12 | Agent入口 |
| W093 | 监控告警整理器 | 告警太多，不知道哪个需要行动 | 聚合告警文本、按服务/严重度/重复度生成处理顺序 | RW08/RW09 | Agent入口 |
| W094 | 变更影响面分析入口 | 改一个配置或依赖影响哪些模块不清楚 | 从 diff、依赖图、路由和配置生成影响面提示 | RW04/RW15 | Agent入口 |
| W095 | 任务分解与验收标准生成器 | 需求描述模糊导致实现和验收偏差 | 从需求文本生成范围、非目标、验收、测试和风险 | RW05/RW15 | Agent入口 |
| W096 | 个人开发节奏面板 | 同时处理多任务时上下文容易丢 | 当前任务、阻塞、下一步、最近命令、相关文件摘要 | RW05 | Agent入口 |
| W097 | 学习/调研资料整理器 | 技术资料、链接、论文、帖子看完无法沉淀 | 链接收纳、要点摘要、对比表、可实践任务 | RW05 | Agent入口 |
| W098 | 工具候选评估表 | 新功能很多，不知道先做哪个 | 按本地性、频率、敏感度、Agent 替代性、成本打分排序 | RW04/RW05 | 值得做 |
| W099 | 完整 AI Chat 模块 | 通用聊天会偏离工具平台，且结果需要验证 | 不做通用 AI 聊天，只做工具到 Agent 的证据入口 | RW10 | 暂不建议 |
| W100 | 完整 DevOps/平台工程套件 | 平台化会引入账号、权限、部署、监控大复杂度 | 当前只做本地分析、轻量入口和 Agent 证据包 | RW09/RW12 | 暂不建议 |
