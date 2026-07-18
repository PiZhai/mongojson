# Steward Tool Authoring Standard 1.0

本规范同时是 R5.0 工具作者契约、Toolsmith 选择依据和工具验证器的外部说明。工具版本目录不可原地修改；任何更新都发布新语义版本，经过依赖准备、真实测试、Schema 验证、SBOM 与来源记录后再原子启用。

## 决策顺序

模型按以下顺序比较方案，并在 Manifest 的 `dependency_strategy` 中记录选择、替代方案和否决理由：

1. 复用现有工具或 Windows 原生 API。
2. 用基础工具构成 `composite` DAG。
3. 使用 PowerShell、Python 或 Node.js 标准库。
4. 工具版本目录内隔离依赖。
5. 全局可调用但内部隔离的 CLI（例如 pipx）。
6. 受治理共享依赖。
7. 机器级全局安装。

全局安装是允许的能力上限，不是快捷默认值。选择全局方案时必须说明隔离方案为何不适用，并记录精确包 ID、来源、版本、安装前状态、哈希、依赖工具和卸载或降级命令。该记录不会触发人工审批。

## Manifest 最小契约

Manifest 必须声明名称、语义版本、运行时、执行位置、输入/输出 JSON Schema、入口及全部源码、精确依赖、依赖策略、超时、取消、幂等语义、输出上限和至少一个真实测试。包生成 CycloneDX SBOM 与 SLSA 风格来源记录。`system` 在服务会话运行，`session` 通过登录会话 Companion 运行，`auto` 由平台按能力与在线状态路由。

## 标准示例

### 1. 文件整理组合工具

使用 `composite_steps` 组合 `fs.search → fs.create_directory → fs.move`，通过 `depends_on` 明确顺序，通过参数绑定传递搜索结果。不创建脚本，不安装依赖；策略为 `none`。这是批量整理文件的首选实现。

### 2. Python 文档元数据提取

运行时为 `python`，依赖放在版本目录 `.venv`。包必须提供包含直接和传递依赖精确版本及 `--hash=sha256:...` 的 `requirements.lock`；安装固定使用 `python -m pip install --require-hashes -r requirements.lock`，执行直接调用 `.venv\Scripts\python.exe`。SBOM 记录全部解析依赖。若它只是独立 CLI，优先比较 pipx。

### 3. Node.js HTML 处理

运行时为 `node`，包同时提交 `package.json` 与 `package-lock.json`。恢复固定使用 `npm ci`，依赖仅位于工具目录 `node_modules`，记录 Node、npm 和 lockfile 版本；不得以全局 npm 包替代锁文件。

### 4. PowerShell Windows 模块

优先使用内置模块。第三方模块以精确版本执行 `Save-PSResource -Path <tool-version> -AuthenticodeCheck`，Tool Host 只在该调用的 `PSModulePath` 中加入包内模块目录。仅服务或多个工具确需共享时选择 `AllUsers`。

### 5. 全局 CLI

仅当多个工具依赖同一个稳定 CLI 且必须由全机 PATH 解析时选择 `global`。Manifest 必须列出 `stdlib`、`isolated` 等被否决方案，以及安装前版本、精确来源、哈希、所有依赖工具和回滚命令。升级前先检查依赖工具兼容性。

### 6. WinGet 机器软件

使用精确 package ID、source、version 与 scope，不能用模糊显示名称。安装前保存状态；可声明时生成 WinGet Configuration、前置条件和依赖关系。若源无法提供精确版本，记录实际解析版本、安装器来源和哈希。

### 7. 禁止反例

- 无版本或无哈希的 `pip install`。
- 无 `package-lock.json` 的 `npm install`。
- 直接信任 `latest`。
- 修改已发布工具版本目录。
- 下载来源不明或无哈希的 EXE。
- 测试失败、输出不符合 Schema 或取消失效时仍启用。
- 未先搜索目录就创建与现有工具重复的脚本。

## 发布与恢复

`tool.create` / `tool.update` 先写入 staging，保存 Manifest、哈希、SBOM 和 provenance，再准备依赖并运行测试。全部通过才原子切换活跃版本和 catalog generation；失败版本保持不可用，已发生的依赖变化按逆序回滚。执行中的调用固定使用开始时的版本，新调用使用新快照。全局暂停、急停、Watchdog、Job Object、证据和 Companion 包哈希验证不可由生成工具覆盖。

参考：[Python venv](https://packaging.python.org/en/latest/guides/installing-using-pip-and-virtual-environments/)、[pipx](https://packaging.python.org/en/latest/guides/installing-stand-alone-command-line-tools/)、[pip 可复现安装](https://pip.pypa.io/en/latest/topics/repeatable-installs/)、[npm 本地安装](https://docs.npmjs.com/downloading-and-installing-packages-locally/)、[npm ci](https://docs.npmjs.com/cli/commands/npm-ci/)、[Save-PSResource](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.psresourceget/save-psresource?view=powershellget-3.x)、[WinGet Configuration](https://learn.microsoft.com/en-us/windows/package-manager/configuration/)、[CycloneDX](https://cyclonedx.org/specification/overview/)、[SLSA Provenance](https://slsa.dev/spec/v1.2/provenance)。
