# Personal Tooling Platform

一个浏览器访问的个人工具平台，首期聚焦：

- 标准 JSON 格式化、压缩、校验
- MongoDB JSON 格式化、对比、表格化、Shell 语句整理、字符串转义/还原
- 数据可视化

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

生产环境样板：

- `deploy/docker-compose.prod.yml`
- `deploy/nginx.prod.conf`
- `deploy/.env.prod.example`
- `deploy/deploy-prod.sh`
- `deploy/backup-postgres.sh`

## 当前实现说明

- 前端已完成工作台壳、路由、核心工具页骨架
- 旧版 JSON / MongoDB JSON 关键逻辑已迁移为 TypeScript 模块
- 后端已完成 PostgreSQL、文件上传、任务创建、预设管理、短期留存框架
- 文档转换方向保留在项目规划中，但当前构建未启用相关前后端能力

## 当前边界

- 首期仍是单体 Go API + 内置 worker，不拆独立队列服务
- 文件落盘存储，数据库只保存元信息和过期时间
- 访问控制预期由 Nginx 层承担，应用层暂不实现账号体系
- 文档转换、更多个人电脑组件接入，作为后续阶段能力继续追加
