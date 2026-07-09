# 日常生活模块级预备需求列表

本文档重新整理日常生活场景的 100 个模块级需求。每一项都按“较完整的小工具或功能模块”组织，避免把同一模块拆成过细的单点提醒。

筛选原则：

- 优先保留能降低日常心智负担、可本地记录、隐私友好、轻量可维护的模块。
- 涉及医疗、金融、法律、关系决策等高风险事项时，只做记录、提醒、准备材料或 Agent 入口，不做替用户下结论的自动决策。
- 避免做大而全生活平台；每个模块应能独立存在，也能接入“生活收纳箱/备忘录/提醒”主线。
- 功能重合时做合并，例如“换滤芯、家电保养、说明书、保修”合并为家庭资产维护模块；“菜谱、购物、库存、临期”合并为厨房与饮食模块群。

## 样本来源索引

| 标签 | 来源与信号 |
|---|---|
| RL01 | Reddit productivity：用户希望有一个能收纳清单、食谱、生日、购物、旅行、礼物等生活信息的地方，但也担心全能 App 做不好。[organize my life](https://www.reddit.com/r/productivity/comments/1bazn1r/is_there_an_app_where_i_can_aesthetically/) |
| RL02 | Reddit simpleliving：过度优化、过多 App 和复杂流程会增加生活负担，简单系统更有价值。[overcomplicated](https://www.reddit.com/r/simpleliving/comments/1pxozfp/anyone_else_tired_of_everything_being/)、[stopped optimizing](https://www.reddit.com/r/simpleliving/comments/1kjjwhk/i_stopped_trying_to_optimize_my_life_and_it_feels/) |
| RL03 | Reddit Adulting：家务、账单、工作、做饭、社交、健康同时压来，成年人日常持续疲惫。[adulting difficulty](https://www.reddit.com/r/Adulting/comments/11mq19r/what_is_the_most_difficult_part_about_adulting/)、[life never stops](https://www.reddit.com/r/Adulting/comments/1toqxx0/anyone_else_just_tired_all_the_time_because_life/) |
| RL04 | BLS American Time Use Survey：家务、做饭、清洁、洗衣等占据大量日常时间。[ATUS](https://www.bls.gov/news.release/atus.nr0.htm)、[household activities](https://www.bls.gov/charts/american-time-use/activity-by-hldh.htm) |
| RL05 | Pew 家务与 mental load：家庭责任、家务、财务管理和孩子日程存在认知负担与分工差异。[Pew 2021](https://www.pewresearch.org/short-reads/2021/01/25/for-american-couples-gender-gaps-in-sharing-household-responsibilities-persist-amid-pandemic/)、[Pew 2023](https://www.pewresearch.org/social-trends/2023/04/13/in-a-growing-share-of-u-s-marriages-husbands-and-wives-earn-about-the-same/) |
| RL06 | Reddit Adulting/house：房屋维护、户外、维修、家电问题长期消耗精力。[owning a house is tiring](https://www.reddit.com/r/Adulting/comments/1ck3lxb/owning_a_house_is_tiring/) |
| RL07 | Reddit Cooking：做饭、买菜、库存、临期、菜谱选择是高频痛点。[meal planning hard](https://www.reddit.com/r/Cooking/comments/10vs1oy/is_it_just_me_or_is_grocery_shoppingmeal_planning/)、[pantry app](https://www.reddit.com/r/Cooking/comments/1i1jbo4/its_been_very_hard_to_find_a_pantry_and_recipe/) |
| RL08 | Reddit personalfinance 与订阅疲劳：预算、订阅、支出追踪、续费提醒是持续需求。[budget app](https://www.reddit.com/r/personalfinance/comments/1hufwqt/whats_the_best_budgeting_app_out_there/)、[Rocket Money](https://www.reddit.com/r/personalfinance/comments/1pxx4xm/is_rocket_money_worth_it/) |
| RL09 | NIQ/订阅疲劳：生活成本和订阅负担让消费者更关注可见性与控制。[consumer outlook](https://nielseniq.com/global/en/insights/report/2025/consumer-outlook-guide-to-2026/)、[subscription fatigue](https://kadence.com/en-us/knowledge/reshaping-product-launches-in-a-world-of-subscription-fatigue/) |
| RL10 | Reddit BuyItForLife：家庭物品、说明书、保修、收据、型号、耐用品比较需要系统管理。[new home binder](https://www.reddit.com/r/BuyItForLife/comments/1lmknb6/list_of_items_to_buy_for_new_home/) |
| RL11 | Reddit travel：旅行准备依赖详细 master packing list、订单、证件和行程整理。[trip prep](https://www.reddit.com/r/travel/comments/16lzwfc/what_are_your_best_tips_to_prepare_for_an/)、[packing essentials](https://www.reddit.com/r/travel/comments/11e5l54/what_do_you_always_forget_to_pack/) |
| RL12 | Reddit simpleliving 健康与习惯：健康追踪不应过度数据化，轻量记录和自我感知更重要。[health tracking critique](https://www.reddit.com/r/simpleliving/comments/196f8db/health_tracking_and_collecting_data_with/) |
| RL13 | APA/Stress in America：金钱、健康、社会压力与连接感影响日常生活。[APA 2025](https://www.apa.org/pubs/reports/stress-in-america/2025)、[money stress](https://www.apa.org/monitor/2015/04/money-stress) |
| RL14 | Reddit selfhosted/dashboard：个人仪表盘容易过度噪音，行动提醒比炫酷展示更有价值。[dashboard noise](https://www.reddit.com/r/selfhosted/comments/1r1wjch/i_used_to_think_dashboards_were_dumb/) |
| RL15 | Reddit simpleliving 决策疲劳：减少日常决策、流程化和“足够好”是强需求。[decision fatigue](https://www.reddit.com/r/simpleliving/comments/1dtl1e9/how_do_you_reduce_the_amount_of_decision_making/) |
| RL16 | Hacker News 生活组织讨论：个人项目管理、任务、工作与生活空间混合管理是高频自建系统场景。[organize your life](https://news.ycombinator.com/item?id=37869517) |
| RL17 | Hacker News 轻量生产力系统：单文件、简单列表、低结构记录长期有效，反证过度复杂工具风险。[single txt file](https://news.ycombinator.com/item?id=22276184)、[productivity system](https://news.ycombinator.com/item?id=32886288) |
| RL18 | Hacker News 小任务压力讨论：大量小任务需要外部化成列表并拆解，降低记忆负担。[small tasks list](https://news.ycombinator.com/item?id=36754312) |
| RL19 | Hacker News TODO 批评：把整个人生塞进 todo app 不现实，支持“轻量模块而非大平台”的边界。[todo apps](https://news.ycombinator.com/item?id=36745858) |
| RL20 | Hacker News 家庭记录工具：Micasa 这类本地 SQLite、无云、无账号、无订阅的 home tracking 工具得到关注。[Micasa](https://news.ycombinator.com/item?id=47075124) |
| RL21 | Hacker News 房屋成本讨论：房屋维护不仅是钱，更消耗大量周末时间和精力。[home ownership cost](https://news.ycombinator.com/item?id=48281611) |
| RL22 | Software Recommendations Stack Exchange：周期性家务提醒跨设备同步是明确需求。[chore reminders](https://softwarerecs.stackexchange.com/questions/87859/reminder-software-for-regular-chores-around-the-house) |
| RL23 | Bogleheads 预算讨论：预算、支出追踪、现金流理解常通过表格或 App 解决。[budget forum](https://www.bogleheads.org/forum/viewtopic.php?t=422900)、[household budgeting](https://www.bogleheads.org/wiki/Household_budgeting) |
| RL24 | Bogleheads/Reddit 预算软件讨论：用户在 App、表格、账户同步和手工记录之间权衡。[budget software](https://www.reddit.com/r/Bogleheads/comments/1gjo3yf/favorite_budget_software/)、[budget apps](https://www.reddit.com/r/Bogleheads/comments/1km3i19/budgeting_softwareapps/) |
| RL25 | NerdWallet 预算 App 评测： recurring bills、subscription manager、分类规则是成熟预算工具常见能力。[budget apps](https://www.nerdwallet.com/finance/learn/best-budget-apps) |
| RL26 | Reddit homeowners：家电收据、保修、说明书、维修单据需要可追踪档案。[receipts warranties](https://www.reddit.com/r/homeowners/comments/1p6gwrv/homeowners_how_do_you_store_appliance_receipts/)、[manuals warranties](https://www.reddit.com/r/homeowners/comments/1o9c9hj/is_there_a_master_app_for_all_the_manuals_and/) |
| RL27 | Reddit homeowners home inventory：保险理赔需要照片、视频、收据、价值和物品细节。[home inventory](https://www.reddit.com/r/homeowners/comments/1netw6f/has_anyone_created_a_home_inventory_for_insurance/)、[inventory first time](https://www.reddit.com/r/homeowners/comments/tu2j91/home_inventory/) |
| RL28 | NAIC/保险部门家庭库存建议：家庭资产清单、条码、照片和文档支撑理赔准备。[NAIC home inventory](https://content.naic.org/consumer/home-inventory)、[CA insurance guide](https://www.insurance.ca.gov/01-consumers/105-type/95-guides/03-res/upload/Website-Version-Home-Inventory_Revision-September-17th-2.pdf) |
| RL29 | Home Assistant Community：家庭仪表盘要按使用场景、设备和家庭成员简化，不应暴露所有控制。[dashboard per user](https://community.home-assistant.io/t/dashboard-for-each-person/742452)、[20 HA lessons](https://community.home-assistant.io/t/20-things-i-wished-i-knew-when-i-started-with-home-assistant/576359) |
| RL30 | Home Assistant/Reddit 家庭仪表盘：家庭成员需要无需技术背景、只显示必要信息的墙面/家庭面板。[wall dashboard](https://www.reddit.com/r/homeassistant/comments/1tzl62z/wallmounted_home_assistant_dashboard_for_family/) |
| RL31 | Apartment Therapy 家务清单经验：Notes、白板、纸面清单等低摩擦方案适合家务周期管理。[notes chore list](https://www.apartmenttherapy.com/notes-app-chore-list-37196476) |
| RL32 | Opendoor/NAR 家庭维护清单：月度、季度、季节性维护可防止维修成本上升。[Opendoor checklist](https://www.opendoor.com/articles/home-maintenance-checklist)、[NAR checklist](https://www.nar.realtor/news/styled-staged-sold/home-maintenance-checklist-to-avoid-costly-repairs) |
| RL33 | FlyerTalk 旅行论坛：packing list、按包分类、低重量行李、多次旅行计划是长期讨论主题。[packing tips](https://www.flyertalk.com/forum/travelbuzz/937866-30-travel-tips-safety-packing-etiquette.html)、[low carry-on weight](https://www.flyertalk.com/forum/travelbuzz/2147740-travel-solutions-low-carry-weight.html) |
| RL34 | Tripadvisor/旅行博客：多目的地旅行计划、行程保存、旅行准备耗时是常见痛点。[Tripadvisor planning](https://www.tripadvisor.com/ShowTopic-g1-i12104-k14741622-Improving_trip_planning_for_trip_with_multiple_destinations-Help_us_make_Tripadvisor_better.html)、[trip planning pain](https://goingawesomeplaces.com/planning-trips-is-a-pain-in-the-ass-itinerary/) |
| RL35 | Good Housekeeping 家务 mental load 调查：家务贡献、mental load、工作生活平衡和 burnout 之间关系明显。[household survey](https://www.goodhousekeeping.com/life/a71485354/splitting-chores-relationship-survey/) |
| RL36 | Mumsnet mental load 讨论：即使“分担家务”，记忆、计划、安排、预判仍可能集中在一个人身上。[mental load](https://www.mumsnet.com/talk/parenting/5435179-how-do-you-explain-mental-load-to-your-partner)、[chores survey](https://www.mumsnet.com/articles/chores-survey-the-truth-about-who-does-what) |
| RL37 | ADHD/家务 App 讨论：Sweepy 这类按 effort、房间状态、可承受任务量推荐的设计能缓解 overwhelm。[ADHD women chores](https://www.reddit.com/r/adhdwomen/comments/165dj0n/app_that_distributes_mental_load_equally_for/) |
| RL38 | App Store/Google Play 家务产品：Home Tasker、OurFlat、Flatastic 等说明家务、账单、购物、分工是一类成熟但仍有需求的模块。[Home Tasker](https://apps.apple.com/us/app/house-chores-cleaning-schedule/id1604578415)、[OurFlat](https://apps.apple.com/ch/app/ourflat-household-chores/id1530007409)、[Flatastic](https://www.flatastic-app.com/) |
| RL39 | 家庭 mental load 产品：KinSync、PAM 等强调 voice-first brain dump、共享 household plan、减少家庭协调负担。[KinSync](https://apps.apple.com/us/app/household-admin-mental-load/id6757087898)、[PAM](https://www.businesswire.com/news/home/20260409710807/en/PAM-Launches-in-the-US-to-Help-Busy-Parents-Tackle-the-Mental-Load-of-Daily-Coordination) |
| RL40 | 家务 App 评测：Tody、Sweepy、Spotless、Homey、Clutterfree 等验证房间、频率、参与者、压力感控制是成熟设计要素。[cleaning apps](https://camillestyles.com/wellness/best-cleaning-apps/) |
| RL41 | Home maintenance app 市场：维修、提醒、DIY、记录、预算等家庭维护模块已有成熟需求。[First American](https://homewarranty.firstam.com/blog/best-home-maintenance-apps)、[Nestfully](https://www.nestfully.com/blog/best-home-maintenance-apps)、[Select Home Warranty](https://www.selecthomewarranty.com/blog/best-home-maintenance-apps/) |
| RL42 | Homeowners 维护提醒：用户用 Microsoft To-do 等工具记录滤芯、汽车胎压、维护周期，证明周期提醒是基础痛点。[maintenance reminders](https://www.reddit.com/r/homeowners/comments/n3zr48/app_for_house_maintenance_reminders/) |
| RL43 | 家庭库存/保险工具市场：Itemopia、United Policyholders、Nest Egg 等强调物品、保修、文档和理赔准备。[UPHelp](https://uphelp.org/5-effective-household-apps-to-manage-your-home-without-the-weariness/)、[SageSure](https://sagesure.com/insurance-insights/home-inventory-apps-to-document-your-possessions/) |
| RL44 | 家庭维护清单博客与方法：年度卡片盒、月度/季度/季节性清单说明“周期任务库”是可复用模块。[annual chores](https://thesensiblehome.wordpress.com/2014/03/30/organizing-chaos-my-annual-household-chore-system/)、[Kelley Nan](https://kelleynan.com/home-maintenance-checklist/)、[HSH](https://www.hsh.com/homeowner/home-maintenance-checklist.html) |
| RL45 | 预算/订阅管理文章：递归账单、订阅、共享支出、季节性费用可以用“订阅视角”统一追踪。[subscription tracker](https://amounthly.com/blog/track-expenses-subscription-tracker)、[FinancialAha](https://www.financialaha.com/articles/track-subscriptions-recurring-expenses/) |
| RL46 | 生活管理产品文章：shared calendar、to-do、home binder、home maintenance、birthdays、bills 和 laundry filters 是成人生活管理常见组合。[life management tools](https://eeva.ai/blog/best-life-management-tools/) |
| RL47 | 旅行打包专业内容：Eagle Creek、FlyerTalk、Tripadvisor 等说明 packing list、按包/场景分类和轻量行李是持久需求。[Eagle Creek](https://eaglecreek.com/blogs/articles/what-pack-ultimate-travel-packing-checklist)、[Tripadvisor packing](https://www.tripadvisor.com/Articles-lp82DUeUkYhw-Best_travel_packing_hacks_tips.html) |
| RL48 | 旅行计划论坛：多目的地、多个旅行并行、行程不宜过满、保留缓冲是高频经验。[Tripadvisor multi-trip](https://www.tripadvisor.co.uk/ShowTopic-g1-i49577-k14709306-Strategies_For_Planning_Multiple_Trips-The_Layover_Lounge.html)、[JapanTravel](https://www.reddit.com/r/JapanTravel/) |
| RL49 | 生活复杂度讨论：HN 与 simpleliving 都强调“简单、即时、本地问题”的价值，避免把生活工具做成复杂项目管理。[world too complicated](https://news.ycombinator.com/item?id=48158065)、[relentlessly simplify](https://news.ycombinator.com/item?id=21629806) |
| RL50 | 学术/研究视角：行政型家务劳动和 cognitive household labor 与压力、关系和心理负担相关。[administrative household labor](https://www.mdpi.com/2076-0760/13/8/404)、[cognitive labor](https://pmc.ncbi.nlm.nih.gov/articles/PMC11761833/) |

## 标记说明

| 标记 | 含义 |
|---|---|
| 值得做 | 适合轻量工具化，能本地处理、重复使用、隐私风险低或可控。 |
| Agent入口 | 需要理解上下文、生成计划、整理文本或做决策辅助，适合交给 Agent。 |
| 后续观察 | 有需求但场景不够稳定，或与当前产品主线关联较弱。 |
| 暂不建议 | 涉及高风险决策、复杂平台化或成熟专用软件更适合。 |

## 筛选方法与结论

筛选维度：

- 心智负担：是否减少记忆、计划、协调、重复决策，而不是增加新系统维护成本。
- 周期性：是否是每周、每月、每季、每年会反复发生的生活任务。
- 本地隐私：是否涉及家庭、健康、财务、证件、资产等不适合云端随意同步的数据。
- 模块完整度：是否能作为一个清晰的小工具存在，而不是单个提醒或单张表。
- Agent 适配度：是否需要综合偏好、预算、时间、家庭情况生成计划或摘要。
- 高风险边界：医疗、金融、法律、关系等只做记录和准备，不做替用户决策。

筛选后的优先队列：

| 优先级 | 模块 ID | 结论 |
|---|---|---|
| P0 生活入口 | L001, L002, L003, L004, L005, L007, L009, L010 | 统一收纳、今日面板、轻量待办、重复提醒和流程模板是所有生活模块的底座。 |
| P0 家务与家庭维护 | L011, L014, L017, L018, L019, L030, L039 | 样本来源最密集，且周期性强，适合做成家庭维护/资产记录核心模块。 |
| P0 厨房与饮食 | L021, L022, L023, L024, L028 | 食材库存、菜谱、购物清单和菜单计划互相强关联，应合并成厨房工作台。 |
| P0 财务与订阅 | L031, L032, L033, L035, L038 | 账单、订阅、预算、分账、保修退货高频且结构化，适合本地轻量管理。 |
| P1 出行与证件 | L041, L042, L043, L044, L045, L048 | 出门/旅行/证件/票据是明确场景，可从模板和粘贴解析切入。 |
| P1 健康记录 | L051, L052, L053, L055, L058 | 只做提醒、记录、就医准备和紧急信息，不做诊断。 |
| P1 关系与家庭协作 | L061, L062, L063, L065, L066, L067, L069 | 适合做提醒、偏好卡、共享摘要，不做监控式协作。 |
| P1 学习与个人资料 | L071, L072, L073, L074, L076, L080 | 可与 MemoDocs/Agent 总结连接，适合作为生活知识整理入口。 |
| P1 数字生活 | L081, L082, L083, L084, L086, L087, L090 | 服务目录、重要文件、备份、设备目录、链接面板和本地数据策略价值稳定。 |
| P2 观察 | L012, L015, L027, L046, L054, L056, L057, L059, L068, L075, L078, L085, L095, L097 | 有场景，但需要更多个人使用验证，先不放入第一批实现。 |
| 暂不建议 | L040, L050, L060, L070, L079, L088, L100 | 避免做智能家居平台、实时导航抢票、医疗诊断、密码库、泛化生活 AI。 |

当前推荐落地顺序：

1. 生活收纳箱 + 今日生活面板。
2. 家务轮转 + 家庭维护/资产档案。
3. 厨房库存 + 菜谱到购物清单。
4. 账单/订阅/保修/退换货管理。
5. 出门/旅行/证件/票据清单。
6. 健康事项提醒与就医记录。

## 100 个模块级需求

| ID | 模块/小工具 | 主要痛点 | 核心功能范围 | 来源标签 | 建议 |
|---|---|---|---|---|---|
| L001 | 生活收纳箱 | 待办、灵感、购物、账单、链接和截图描述散落 | 快速捕捉、自动分类、标签、搜索、归档和导出 | RL01/RL02 | 值得做 |
| L002 | 今日生活面板 | 每天该处理什么不清楚，信息分散 | 汇总今日待办、账单、家务、健康、出行和提醒 | RL03/RL14 | 值得做 |
| L003 | 轻量待办与减压清单 | 任务列表太长反而增加焦虑 | 按必须做、顺手做、可延期分层，支持低精力模式 | RL02/RL03 | 值得做 |
| L004 | 高级重复提醒中心 | 每周、月底、工作日、周期性任务规则复杂 | 支持复杂周期、上次完成推算、跳过和延期 | RL03/RL04 | 值得做 |
| L005 | 生活 Inbox 分类器 | 随手记录混在一起，之后难整理 | 将文本归类为待办、购买、账单、旅行、学习、关系和家庭 | RL01 | Agent入口 |
| L006 | 周计划与周回顾生成器 | 一周计划和实际执行偏离后缺少复盘 | 生成周计划、回顾完成情况、拖延原因和下周调整 | RL02/RL03 | Agent入口 |
| L007 | 生活流程模板库 | 早晚、出门、睡前、周末重复流程每次想一遍 | 可复用流程模板、checklist、执行记录和调整建议 | RL02/RL15 | 值得做 |
| L008 | 决策降噪助手 | 日常选择太多造成决策疲劳 | 将选择限制到少数方案，记录标准、取舍和冷静期 | RL15 | Agent入口 |
| L009 | 简单生活仪表盘 | 仪表盘容易花哨但不产生行动 | 只展示异常、到期、今天必须处理的事项 | RL02/RL14 | 值得做 |
| L010 | 生活系统维护器 | 工具越用越复杂，最后变成新负担 | 定期清理无用模块、归档旧清单、检查提醒噪音 | RL02/RL14 | 值得做 |
| L011 | 家务轮转系统 | 家务永远做不完，缺少可持续节奏 | 房间/任务/频率/负责人/上次完成记录和下一次提醒 | RL03/RL04/RL05 | 值得做 |
| L012 | 家务分工与公平看板 | 家庭成员对谁做了多少认知不一致 | 任务分配、完成历史、负担统计、共享摘要 | RL05 | 后续观察 |
| L013 | 房间清洁计划器 | 深度清洁不知道从哪开始 | 按房间生成日常、每周、月度、深度清洁流程 | RL04/RL06 | Agent入口 |
| L014 | 家庭耗材库存 | 纸巾、洗衣液、灯泡、电池等用完才发现 | 耗材清单、最低库存、补货提醒、购买记录 | RL03/RL06 | 值得做 |
| L015 | 衣物与洗护管理器 | 洗衣、换季、干洗、修补、收纳容易混乱 | 洗护标签、换季清单、修补记录、衣物库存 | RL04 | 后续观察 |
| L016 | 垃圾与回收日历 | 垃圾、回收、特殊废弃物时间容易忘 | 分类规则、日历提醒、特殊处理说明和地点记录 | RL03/RL04 | 值得做 |
| L017 | 家庭维修工单簿 | 漏水、损坏、维修沟通和费用记录散落 | 问题记录、照片位置、处理状态、联系人、费用和复盘 | RL06 | 值得做 |
| L018 | 家庭维护检查清单 | 水管、门窗、空调、烟感等长期维护被忽略 | 季度/年度检查模板、异常记录、下一步建议 | RL06/RL10 | Agent入口 |
| L019 | 家庭服务商目录 | 维修、保洁、物业、快递、安装人员联系方式难找 | 服务商、价格、评价、历史记录、保修和下次联系 | RL06/RL10 | 值得做 |
| L020 | 搬家与空间整理工具 | 打包、断舍离、箱号、物品位置很混乱 | 箱号清单、房间映射、捐赠/丢弃/保留决策和进度 | RL01/RL06 | 值得做 |
| L021 | 食材库存与临期管理 | 冰箱、冷冻、 pantry 中有什么经常忘 | 食材库存、有效期、位置、数量、临期提醒和消耗优先级 | RL07 | 值得做 |
| L022 | 菜谱收纳与标准化 | 食谱来自网页、视频、笔记，格式不统一 | 保存来源、提取食材步骤、份量换算、标签和备注 | RL07 | 值得做 |
| L023 | 菜谱到购物清单生成器 | 菜谱和买菜清单脱节 | 多菜谱合并食材、去重、按超市区域分组、现有库存抵扣 | RL07 | 值得做 |
| L024 | 一周菜单计划器 | 每天吃什么耗费大量心智 | 基于时间、预算、库存、口味和营养偏好生成菜单草稿 | RL07/RL15 | Agent入口 |
| L025 | 批量备餐计划器 | Meal prep 想做但步骤和保存计划复杂 | 份数、保存天数、烹饪顺序、复热方式和购物清单 | RL07 | Agent入口 |
| L026 | 厨房清理流程工具 | 做饭后的清洁和收尾经常拖延 | 烹饪后 checklist、剩菜处理、台面、垃圾、餐具和炉灶清理 | RL03/RL07 | 值得做 |
| L027 | 食品浪费分析器 | 买了不用、临期扔掉，浪费原因不清 | 记录丢弃食物、原因、价格和下次购买建议 | RL07/RL09 | 后续观察 |
| L028 | 常备品补货系统 | 常用食材和日用品补货靠记忆 | 常备清单、最低库存、替代品、购买周期和价格备注 | RL07/RL08 | 值得做 |
| L029 | 外卖与下厨决策助手 | 忙时不知道该做饭还是点外卖 | 估算时间、库存、预算、疲劳程度，给出低负担选项 | RL03/RL15 | Agent入口 |
| L030 | 厨房设备与耗材维护 | 滤芯、锅具、刀具、小家电保养容易忘 | 设备清单、清洁周期、耗材型号、保养记录 | RL06/RL10 | 值得做 |
| L031 | 账单与缴费日历 | 房租、水电、信用卡、保险日期分散 | 周期账单、金额、扣款方式、提醒、已支付记录 | RL03/RL08/RL13 | 值得做 |
| L032 | 订阅管理器 | 订阅服务越来越多，续费和取消容易忘 | 订阅清单、续费日、价格、用途、取消入口、年度成本 | RL08/RL09 | 值得做 |
| L033 | 轻量预算与现金流面板 | 预算工具太复杂，但又想知道钱去哪了 | 固定支出、可变支出、日均可用、分类趋势和提醒 | RL08/RL13 | 值得做 |
| L034 | 支出文本解析器 | 银行短信、账单明细、收据手工录入麻烦 | 粘贴文本提取金额、商户、日期、类别和备注 | RL08 | Agent入口 |
| L035 | 共同支出分账工具 | 室友、伴侣、朋友共同消费结算麻烦 | 分摊规则、付款人、结算摘要、历史记录和导出 | RL08/RL05 | 值得做 |
| L036 | 购买冷静期清单 | 冲动消费后悔，商品收藏散落 | 想买原因、价格、替代方案、等待期、最终决定记录 | RL02/RL08 | 值得做 |
| L037 | 商品对比决策表 | 大件购买规格、价格、评价难比较 | 参数表、优缺点、总拥有成本、保修和风险备注 | RL10/RL15 | Agent入口 |
| L038 | 保修/发票/退换货管理 | 发票、保修期、退货期限经常找不到 | 物品、购买日期、保修、发票位置、退货截止和提醒 | RL10 | 值得做 |
| L039 | 家庭资产与说明书档案 | 设备型号、说明书、配件规格、维修记录分散 | 资产卡片、型号、说明书链接、耗材、保养和维修记录 | RL10 | 值得做 |
| L040 | 投资/保险/贷款决策工具 | 金融建议高风险，不应由轻量工具自动判断 | 只做资料清单、问题列表、比较表，不做投资或保险建议 | RL08/RL13 | 暂不建议 |
| L041 | 出门清单生成器 | 通勤、健身、看病、办事、旅行常忘带东西 | 按场景生成可复用 checklist，支持季节、天气和目的地 | RL11/RL15 | 值得做 |
| L042 | 旅行打包清单系统 | 每次旅行从头写，细节容易漏 | master packing list、按天数/天气/活动调整、包内分区 | RL11 | 值得做 |
| L043 | 旅行订单收纳箱 | 机票、酒店、车票、门票散落在邮件和截图 | 粘贴/记录订单，提取时间、地点、订单号、地址和联系人 | RL11 | Agent入口 |
| L044 | 行程压力检查器 | 旅行日程排太满，交通和开放时间冲突 | 估算交通、缓冲、开放时间、地理顺序和风险 | RL11/RL15 | Agent入口 |
| L045 | 证件到期管理器 | 身份证、护照、签证、驾照、保险容易过期 | 有效期、办理周期、材料 checklist、提前提醒 | RL11 | 值得做 |
| L046 | 通勤与常用路线档案 | 常用路线、停车、备用方案临时找不到 | 路线、耗时、费用、停车位置、备用方案和注意事项 | RL03 | 后续观察 |
| L047 | 车辆事项管理器 | 保养、保险、年检、停车、违章记录分散 | 车辆档案、保养周期、费用、证件、保险和提醒 | RL06/RL10 | 值得做 |
| L048 | 票据与报销收纳箱 | 票据、发票、报销状态容易丢 | 票据信息、图片位置、金额、用途、是否报销和截止日 | RL08/RL11 | 值得做 |
| L049 | 办事材料清单生成器 | 政务、银行、学校、医院办事材料复杂 | 事项、材料、流程、预约、进度和下次步骤 | RL03 | Agent入口 |
| L050 | 实时导航/抢票平台 | 需要实时数据和复杂接入，已有成熟工具 | 不做地图导航、实时交通、票务抢购，只做行前整理 | RL11 | 暂不建议 |
| L051 | 健康事项提醒簿 | 体检、牙科、眼科、疫苗、复诊日期容易忘 | 健康事件、周期、提醒、结果附件位置、下次问题 | RL12/RL13 | 值得做 |
| L052 | 药箱与有效期管理 | 药品用途、剂量、有效期和库存难记 | 药品清单、有效期、用途备注、补货提醒和禁忌备注 | RL12 | 值得做 |
| L053 | 症状与就医记录 | 看医生时说不清症状变化和用药过程 | 时间线、强度、诱因、用药、图片位置和导出摘要 | RL12/RL13 | 值得做 |
| L054 | 健康习惯轻记录 | 睡眠、运动、饮水记录太重难坚持 | 手动轻记录、趋势、备注，不追求过度量化 | RL12/RL02 | 后续观察 |
| L055 | 低精力生活维护模式 | 疲惫、生病、忙碌时仍有基本生活事项 | 最低行动清单、饮食、清洁、药物、休息和必要沟通 | RL03/RL12 | 值得做 |
| L056 | 运动计划草稿器 | 想锻炼但不知道怎么排计划 | 根据目标、时间、限制生成训练计划草稿和记录模板 | RL12 | Agent入口 |
| L057 | 饮食观察日志 | 饮食和身体感受有关但不想复杂称重 | 餐次、主观感受、诱因、过敏/不适记录和摘要 | RL12/RL07 | 后续观察 |
| L058 | 紧急信息卡 | 紧急联系人、过敏、常用药、保险信息难快速找到 | 本地紧急卡片、打印版、联系人、医疗备注和证件位置 | RL12 | 值得做 |
| L059 | 情绪与压力日志 | 压力、心情波动缺少可回顾线索 | 事件、情绪、想法、应对方式、睡眠和复盘 | RL13/RL02 | 后续观察 |
| L060 | 医疗诊断/用药建议 | 医疗建议高风险，不能由小工具替代医生 | 不做诊断和用药决策，只做记录、提醒和就医准备 | RL12/RL13 | 暂不建议 |
| L061 | 礼物想法库 | 临近节日才想礼物，平时灵感丢失 | 按人物、预算、兴趣、尺码、历史礼物和节日记录 | RL01/RL15 | 值得做 |
| L062 | 关系日期与准备提醒 | 生日、纪念日、节日准备分散 | 日期、提前提醒、礼物/问候模板、历史记录 | RL01 | 值得做 |
| L063 | 人物偏好卡片 | 家人朋友的口味、禁忌、尺码、地址记不住 | 偏好、禁忌、联系方式、地址、重要事项和备注 | RL01 | 值得做 |
| L064 | 聚会与活动筹备器 | 聚会、婚礼协助、生日会要处理很多细节 | 邀请、菜单、购物、预算、场地、清理和进度 | RL01/RL03 | Agent入口 |
| L065 | 家庭共享摘要生成器 | 家庭成员之间信息不同步，但不想做复杂协同系统 | 将购物、家务、行程、账单生成可复制共享摘要 | RL05/RL02 | 值得做 |
| L066 | 承诺事项捕捉器 | 聊天中答应的事之后忘记 | 从文本提取“我答应/需要做”的事项和提醒 | RL01 | Agent入口 |
| L067 | 宠物事项管理器 | 喂食、驱虫、疫苗、洗澡、异常观察容易漏 | 宠物档案、周期提醒、用量、预约、症状和费用记录 | RL03 | 值得做 |
| L068 | 育儿事项模板库 | 育儿物品、预约、证件和日程复杂 | 物品清单、预约提醒、成长记录模板，不做医疗建议 | RL05/RL13 | 后续观察 |
| L069 | 家庭隐私边界设置 | 共享生活数据可能侵入隐私 | 本地优先、按模块导出、默认不做定位和监控 | RL02/RL05 | 值得做 |
| L070 | 家庭聊天/定位平台 | 完整家庭协作会变成社交和监控平台 | 不做聊天、实时定位、监控和复杂账号体系 | RL02/RL05 | 暂不建议 |
| L071 | 学习资料收纳箱 | 课程、文章、视频、笔记和进度散落 | 链接、来源、主题、进度、下次继续位置和复习提醒 | RL01 | 值得做 |
| L072 | 学习目标拆解器 | 想学的东西很多但难落地 | 目标、时间、材料、阶段任务、输出物和复盘 | RL15 | Agent入口 |
| L073 | 阅读笔记卡片 | 读书摘录和行动点难回顾 | 书籍、章节、摘录、观点、行动项和复习日期 | RL01 | 值得做 |
| L074 | 稍后读清理器 | 收藏很多文章但不看，链接过期 | 链接分类、摘要、是否过期、是否值得读和清理建议 | RL02/RL15 | Agent入口 |
| L075 | 兴趣项目看板 | 兴趣项目半途而废，材料和下一步分散 | 目标、材料、下一步、阻塞、照片记录和完成状态 | RL01/RL02 | 后续观察 |
| L076 | 练习日志工具 | 技能练习缺少连续反馈 | 练习时间、内容、问题、改进点、下次任务 | RL12/RL15 | 值得做 |
| L077 | 学习负荷检查器 | 学习计划过满，无法持续 | 估算每周时间、任务容量、优先级和削减建议 | RL02/RL15 | Agent入口 |
| L078 | 词句与灵感卡片 | 语言学习、写作句子、灵感片段容易丢 | 生词、例句、来源、标签、复习和引用 | RL01 | 后续观察 |
| L079 | 个人知识库替代品 | 完整 PKM 容易过度复杂 | 不做双链知识库平台，只做轻量收纳、模板和回顾 | RL02 | 暂不建议 |
| L080 | 学习内容转行动 | 看完教程但没有实践计划 | 从笔记提取实践任务、问题、复习点和下一步 | RL15 | Agent入口 |
| L081 | 个人服务目录 | 账号、会员、服务入口和用途找不到 | 服务名、网址、用途、续费、关联邮箱、备注和风险 | RL01/RL09 | 值得做 |
| L082 | 重要文件索引 | 证件、合同、扫描件、票据散落 | 文件名、位置、用途、到期日、相关联系人和备份状态 | RL10 | 值得做 |
| L083 | 备份检查清单 | 备份做没做、能不能恢复不清楚 | 备份对象、频率、上次验证、恢复步骤和风险 | RL14 | 值得做 |
| L084 | 设备迁移清单 | 换手机/电脑时认证器、照片、聊天记录容易漏 | 迁移步骤、账号、备份、验证、旧设备清理 | RL03 | 值得做 |
| L085 | 截图整理入口 | 截图越存越多，重要信息难找 | OCR/手动标题、标签、来源、是否处理、归档 | RL01 | 后续观察 |
| L086 | 家庭网络与设备目录 | 路由器、NAS、打印机、智能设备入口和 IP 难记 | 设备、位置、IP、管理入口、用途、说明和维护记录 | RL10/RL14 | 值得做 |
| L087 | 个人链接面板 | 常用网站、家庭服务、自托管入口分散 | 本地首页、分组、描述、健康提示和低噪音展示 | RL14 | 值得做 |
| L088 | 密码管理器替代品 | 密码管理高风险，不应自制 | 不保存密码，只记录使用哪个密码管理器、恢复流程和账号线索 | RL13 | 暂不建议 |
| L089 | 数字遗产准备清单 | 重要账号、文件、保险、联系人没有交接计划 | 资料位置、联系人、账号线索、授权说明和打印摘要 | RL13 | 后续观察 |
| L090 | 隐私数据导出与清理器 | 本地生活数据需要可控、可迁移 | 按模块导出、清理旧记录、敏感字段检查和归档 | RL02/RL13 | 值得做 |
| L091 | 任务拆解器 | 大目标让人拖延，不知道第一步 | 将目标拆成 10 分钟行动、材料、阻塞和验收 | RL15 | Agent入口 |
| L092 | 低压力日回顾 | 一天结束后没有整理，但复杂复盘难坚持 | 三件事、未完成、能量、明日重点和简短备注 | RL02/RL03 | 值得做 |
| L093 | 周末安排器 | 周末被家务、采购、社交、休息挤满 | 平衡休息、家务、采购、社交、准备工作的安排草稿 | RL03/RL15 | Agent入口 |
| L094 | 生活节奏审计器 | 不知道时间和精力被哪些琐事消耗 | 轻量记录时间块、痛点、重复任务和可简化建议 | RL02/RL03 | Agent入口 |
| L095 | 分心触发记录器 | 手机、消息、社交媒体打断注意力 | 记录触发、时间、情境、替代动作和复盘 | RL02/RL12 | 后续观察 |
| L096 | “足够好”决策模板 | 追求最优导致拖延和过度研究 | 设定最低标准、截止时间、候选方案和最终选择 | RL02/RL15 | 值得做 |
| L097 | 社交恢复计划器 | 孤独或社交中断时不知道怎么重新连接 | 低压力联系清单、活动想法、频率和跟进提醒 | RL03/RL13 | 后续观察 |
| L098 | 生活异常事件日志 | 生病、维修、突发支出、家庭问题没有记录 | 事件、时间线、费用、联系人、处理结果和复盘 | RL03/RL13 | 值得做 |
| L099 | 生活模块候选评估表 | 想做的生活工具很多，容易再次平台化 | 按频率、隐私、复杂度、现有替代、心智减负评分 | RL02/RL14 | 值得做 |
| L100 | 通用生活 AI 助理 | 泛化助手边界大、隐私和责任不清 | 不做全能生活 AI，只做记录、整理、提醒和 Agent 入口 | RL02/RL13 | 暂不建议 |
