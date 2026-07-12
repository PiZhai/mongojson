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

## 前端模块化规范

前端以“受治理的模块化单体 + 微内核插件架构”为目标，统一定义模块清单、扩展点、
功能开关、依赖边界、可删除性、独立运行与微前端升级条件。详细规则见
[Frontend Modular Platform Architecture Standard](docs/frontend-modular-platform-standard.md)。

模块构建与启停：

```powershell
# 只构建指定模块，未包含模块不会进入 JS/CSS 产物
$env:VITE_INCLUDED_MODULES = 'inspect,json,mongo-json'
npm --prefix frontend run build

# 发布全部模块，但启动时关闭指定模块
$env:VITE_DISABLED_MODULES = 'music,watch-party'
npm --prefix frontend run dev
```

架构验证：

```powershell
npm --prefix frontend run check:architecture
npm --prefix frontend test
npm --prefix frontend run test:module-profiles
```
