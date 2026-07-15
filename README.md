# Personal Tooling Platform

一个浏览器访问的个人工具平台，首期聚焦：

- 智能粘贴诊断：识别 JSON、Mongo Shell、curl、日志片段、NDJSON 和转义字符串，并推荐下一步
- 标准 JSON 格式化、压缩、校验
- MongoDB JSON 格式化、Canonical Extended JSON 输出、语义对比、Schema 体检、Shell 查询风险检查、JSON 修复、字符串转义/还原
- 数据可视化
- 在线备忘录与悬浮卡片

## 项目结构

```text
.
├── backend
├── deploy
├── docs
├── frontend
├── docker-compose.yml
└── index.html
```

> `index.html` 保留为旧版单文件原型，新的正式项目实现位于 `frontend/` 与 `backend/`。

## 本地开发

### Frontend

```bash
cd frontend
npm install
npm run dev
```

默认开发地址：`http://127.0.0.1:4174`

开发态已配置代理：

- `/healthz` -> `http://127.0.0.1:18080/healthz`
- `/readyz` -> `http://127.0.0.1:18080/readyz`
- `/api` -> `http://127.0.0.1:18080`

### Backend

```bash
cd backend
cp .env.example .env
go run ./cmd/server
```

默认本地监听：`http://127.0.0.1:18080`

默认依赖本地 PostgreSQL：

- Host: `localhost`
- Port: `5432`
- DB: `mongojson`
- User: `postgres`
- Password: `postgres`

### Private Steward Software

私人管家可以构建为 Windows、macOS 和 Linux 后台软件目录。每个默认产物同时包含 `steward` CLI/服务二进制、`steward-companion` 用户态加密缓冲进程和 Web 工作台：

```powershell
.\deploy\build-steward.ps1 -Version local
.\deploy\verify-steward-dist.ps1 -ExpectedVersion local -RunCurrentBinary
```

选择当前平台目录后直接运行：

```powershell
# Windows
.\steward.exe run

# macOS / Linux
./steward run
```

默认本地管理地址是 `http://127.0.0.1:18080`，工作台入口是 `http://127.0.0.1:18080/tools/steward`。二进制会自动托管同级 `ui/`，仍需要可用的 PostgreSQL `DATABASE_URL`。从构建发布包到三台真实设备安装、配对和 24 小时验收的完整步骤见 [三端打包、安装与验证教程](docs/personal-ai-steward-three-platform-deployment-guide.md)；自动记录、证据关系和清理规则见 [自动记录、关联记忆与信息生命周期](docs/personal-ai-steward-activity-memory-lifecycle.md)；协议和验收字段定义见 [S3/S4 运行与验证基线](docs/personal-ai-steward-s3-s4-runtime.md)。

工作台支持本机持久化对话，以及 D0-D6 数据策略和 A0-A9 操作策略矩阵。每个数据等级可按来源独立设置禁止、手动或自动采集，独立设置是否发送模型及 `metadata/summary/redacted/raw` 内容形态；每个权限等级可按动作独立设置执行模式、模拟、回滚、批量和冷却。D4-D6 观察、对话和候选使用本地 AES-GCM 加密。D5 凭据检测会先把伪装成低等级的内容提升为 D5；默认仍拒绝，只有 companion 白名单、中央数据策略、A6 模型外发策略和模型最高等级同时允许时才可保存或外发。高权限执行只接受已登记的结构化工具，不把模型文本作为 shell 命令。

目标主机安装先用受保护的服务环境 JSON 做无写入计划；确认后必须从管理员/root 终端显式执行真实安装：

```powershell
.\deploy\run-steward-service-install-e2e.ps1 -PlanOnly -ConfigFile <protected-service-env.json>
.\deploy\run-steward-service-install-e2e.ps1 -ConfirmInstall -BinaryPath <stable-steward-binary> -WorkDir <stable-workdir> -ConfigFile <protected-service-env.json> -EvidenceDir <evidence-dir>
```

真实安装会创建并启动当前平台的原生系统服务，再执行 strict S3/S4 校验和默认 24 小时 watch；不会把配置文件中的密钥写入命令或 evidence。

三台主机各自完成 `service-install-e2e` 和 `run-steward-s3s4-final-host.ps1` 后，使用 inventory 驱动的协调脚本做最终归档和门禁：

```powershell
.\deploy\run-steward-s3s4-final-system.ps1 -InventoryFile .\deploy\steward-s3s4-final-system.json -PlanOnly
.\deploy\run-steward-s3s4-final-system.ps1 -InventoryFile .\deploy\steward-s3s4-final-system.json -BinaryPath <steward-binary> -EvidenceDir <final-evidence-dir>
```

inventory 可从 `deploy/steward-s3s4-final-system.example.json` 开始填写，不应包含任何密钥。协调脚本对三个来源包分别验证平台、agent、system service、advisor 身份和 24 小时 watch，复制时记录 SHA-256，再调用 `s3s4-final-system` preset 生成统一 manifest。

## Docker Compose

```bash
docker compose up --build
```

服务：

- `http://localhost` -> Nginx
- `http://localhost/healthz` -> Nginx 转发后的 Go backend

Compose 运行包含：

- `postgres`
- `backend`
- `frontend`
- `nginx`

## 生产部署

云服务器部署方案见：

- [docs/cloud-deployment-full.md](/Users/administrator/Documents/mongojson/docs/cloud-deployment-full.md)
- [docs/deploy-centos-stream-10.md](/Users/administrator/Documents/mongojson/docs/deploy-centos-stream-10.md)
- [docs/deploy-runbook.md](/Users/administrator/Documents/mongojson/docs/deploy-runbook.md)
- [docs/macos-offline-release.md](/Users/administrator/Documents/mongojson/docs/macos-offline-release.md)

生产环境样板与脚本：

- `deploy/docker-compose.prod.yml`
- `deploy/nginx.prod.conf`
- `deploy/.env.prod.example`
- `deploy/build-push-images.ps1`
- `deploy/deploy-prod.sh`
- `deploy/deploy-init.sh`
- `deploy/deploy-release.sh`
- `deploy/deploy-frontend.sh`
- `deploy/deploy-backend.sh`
- `deploy/deploy-restart.sh`
- `deploy/deploy-status.sh`
- `deploy/docker-prune.sh`
- `deploy/backup-postgres.sh`

推荐阅读顺序：

1. [docs/deploy-centos-stream-10.md](/Users/administrator/Documents/mongojson/docs/deploy-centos-stream-10.md)  
   首次部署，从空机器到服务上线。
2. [docs/deploy-runbook.md](/Users/administrator/Documents/mongojson/docs/deploy-runbook.md)  
   日常发版、仅重启、前后端分开发布。

## 当前实现说明

- 前端已完成工作台壳、路由、智能诊断入口和核心工具页骨架
- 旧版 JSON / MongoDB JSON 关键逻辑已迁移为 TypeScript 模块
- MongoDB JSON 工作台已扩展语义 Diff、Schema 体检、结构生成、Shell 风险检查、显式 JSON 修复和 Extended JSON 复制
- MongoDB 解析核心已增加 `frontend/src/lib/mongodb-core` facade，第三方 BSON / JSON repair / 查询解析依赖只在该边界内使用，并通过 BSON/JS 值到项目 AST 的适配器支撑表格、Diff、Schema 流程
- 在线备忘录已支持云端自动保存、图片上传和悬浮卡片
- 后端已完成 PostgreSQL、文件上传、预设管理、短期留存框架；异步任务底座保留但当前构建默认拒绝创建任务
- 文档转换方向保留在项目规划中，但当前构建未启用相关前后端能力

## 当前边界

- 首期仍是单体 Go API + 内置 worker，不拆独立队列服务；未接入真实处理器前，异步任务 API 不对外承诺可用能力
- 文件落盘存储，数据库只保存元信息和过期时间
- 访问控制预期由 Nginx 层承担，应用层暂不实现账号体系
- 文档转换、更多个人电脑组件接入，作为后续阶段能力继续追加
