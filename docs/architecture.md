# Personal Tooling Platform v1

## Overview

- Frontend: React + TypeScript + Vite
- Backend: Go HTTP API + in-process worker
- Database: PostgreSQL
- Reverse proxy: Nginx
- Deployment: Docker Compose

## Repository Layout

```text
frontend/   React 工作台与 Monaco/ECharts 交互层
backend/    Go API、任务执行、文件管理、定时清理
deploy/     Nginx 配置
docs/       架构和实现说明
```

## Modules

- JSON Tool
- MongoDB JSON Tool
- Visualization Tool

## Backend services

- `file`: upload, metadata, download
- `task`: async jobs, polling, retention cleanup
- `system`: health and readiness

## Runtime Flow

1. 前端上传文件到 `POST /api/files`
2. 前端通过 `POST /api/jobs` 创建处理任务
3. Go 服务将任务写入 PostgreSQL 并投递到内置 worker
4. worker 执行处理任务并写回 `tool_jobs` / `tool_files`
5. 前端轮询 `GET /api/jobs/:id`，成功后通过下载接口获取结果

## Notes

- Files are stored on disk, not inside PostgreSQL.
- Job results are short-lived and cleaned by `expires_at`.
- Access control is expected to be enforced by Nginx in front of the app.
- 文档转换属于后续扩展能力，当前构建未启用。
