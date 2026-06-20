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
- Inspect Tool
- MongoDB JSON Tool
- Visualization Tool

## Backend services

- `file`: upload, metadata, download
- `task`: async job schema, bounded in-process queue, polling, retention cleanup
- `system`: health and readiness

## Runtime Flow

当前构建以浏览器端工具为主，前端提供智能粘贴识别、JSON/MongoDB JSON 诊断、语义 Diff、Schema 体检、Shell 风险检查和可视化预览。后端提供文件、预设、健康检查和任务底座能力。异步 job 处理器尚未启用，因此 `POST /api/jobs` 会对未启用工具返回 `503`，避免 API 声明与实际能力不一致。

后续接入真实异步处理器后的目标流程：

1. 前端上传文件到 `POST /api/files`
2. 前端通过 `POST /api/jobs` 创建处理任务
3. Go 服务将任务写入 PostgreSQL 并非阻塞投递到有界内置 worker 队列
4. worker 执行处理任务并写回 `tool_jobs` / `tool_files`
5. 前端轮询 `GET /api/jobs/:id`，成功后通过下载接口获取结果

## Notes

- Files are stored on disk, not inside PostgreSQL.
- Job results are short-lived and cleaned by `expires_at`.
- The in-process queue is bounded and returns service unavailable instead of blocking request handlers when saturated.
- `/readyz` checks database connectivity, storage writability, and worker status.
- Access control is expected to be enforced by Nginx in front of the app.
- 文档转换属于后续扩展能力，当前构建未启用。
