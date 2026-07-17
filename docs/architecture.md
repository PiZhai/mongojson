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
- Memo Docs Tool

前端模块边界、插件契约、功能开关、可删除性、可独立运行和未来微前端升级的
强制规范见 [Frontend Modular Platform Architecture Standard](frontend-modular-platform-standard.md)。
当前演进目标是受治理的模块化单体加微内核插件架构，而不是直接引入独立部署的微前端。

当前前端组合机制：

- `frontend/module-catalog.json` 定义构建时可发现模块；
- `frontend/src/modules/*/manifest.ts` 声明路由、导航、能力、Provider 和壳层扩展点；
- `VITE_INCLUDED_MODULES` 生成只包含指定模块的构建产物；
- `VITE_DISABLED_MODULES` 在启动时关闭已随构建发布的模块；
- `npm run check:architecture` 检查依赖方向；
- `npm run test:module-profiles` 验证每个模块均可独立构建，且不泄漏其他模块的路由与 CSS。

MongoDB JSON Tool 的下一阶段设计见
[MongoDB Tool Open Source Core Design](mongodb-open-source-core-design.md)，目标是将
Monaco 定位为编辑器交互层，将解析、格式化、修复和基础校验迁移到开源库 facade，
同时保留项目自研的 MongoDB 风险规则和摘要能力。当前代码已经引入
`frontend/src/lib/mongodb-core` 作为 facade，第三方解析、EJSON、jsonrepair
和查询参数校验依赖集中在该目录内，并通过 BSON/JS 值到项目 `JsonNode`
的适配器继续支撑表格、Diff、Schema 等既有工作流。

## Backend services

- `file`: upload, metadata, download
- `task`: async job schema, bounded in-process queue, polling, retention cleanup
- `system`: health and readiness

## Runtime Flow

当前构建以浏览器端工具为主，前端提供智能粘贴识别、JSON/MongoDB JSON 诊断、Canonical Extended JSON 输出、显式 JSON 修复、语义 Diff、Schema 体检、Shell 风险检查、可视化预览、在线备忘录和音乐播放。后端提供文件、备忘录、音乐曲库、预设、健康检查和任务底座能力。异步 job 处理器尚未启用，因此 `POST /api/jobs` 会对未启用工具返回 `503`，避免 API 声明与实际能力不一致。

在线备忘录通过 `GET /api/memo/documents` 获取按更新时间排序的存档文档摘要，并通过 `GET /api/memo/documents/{id}/ws` 订阅文档级变更通知。正文仍由 HTTP API 结合 revision 乐观并发控制持久化，保存成功后 WebSocket 仅广播文档 ID、revision 和变更类型；其他客户端再拉取数据库中的权威快照。侧边便签的新增、修改和删除复用同一频道。本地存在未保存编辑时不会自动覆盖，而是保留恢复快照并提示版本冲突。

音乐模块通过 `POST /api/music/tracks` 持久化上传歌曲和可选的同名 `.lrc` 歌词，并使用音频 SHA-256 指纹阻止重复记录。`GET /api/music/tracks?cursor=...&limit=...` 游标分页读取远程曲库，`GET /api/music/tracks/{id}/content` 获取支持 HTTP Range 的音频内容，`GET /api/music/tracks/{id}/lyrics` 读取歌词，`DELETE /api/music/tracks/{id}` 原子删除歌曲元数据及音频、歌词文件。音乐文件使用独立 `music` / `music-lyrics` 存储分类且不进入临时文件过期清理。

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
