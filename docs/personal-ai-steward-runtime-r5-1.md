# R5.1 自主补救与事务化系统变更

R5.1 在 R4.9 循环 Agent 与 R5.0 工具平台之间增加统一的操作系统变更事务层。目标不是用固定业务规则替模型决策，而是保证模型选择的每种实现都能得到真实错误、可验证结果和可恢复副作用。

## 执行流程

```text
模型调用变更型工具
  → Runtime 持久化最小操作前快照
  → 执行工具
  → 从操作系统重新读取后置状态
  ├─ 验证通过：提交事务，结果返回模型
  └─ 执行或验证失败：分类错误，执行补偿，结果与候选方案返回模型
       → 模型选择不同作用域、设备、工具或实现
```

`steward_system_change_transactions` 保存工具不可变版本、参数、快照、结果、错误分类、回滚结果、租约和重试时间。daemon 使用 `FOR UPDATE SKIP LOCKED` 领取崩溃遗留事务；工具运行期间持续刷新事务租约，避免另一个进程提前回滚仍在进行的调用。

最近事务可通过 `GET /api/steward/runtime/transactions?limit=100` 检查，返回提交、回滚、失败分类和补偿证据；模型下一轮仍直接从工具结果的 `_transaction` / `_remediation` 获取当前调用状态。

## 错误与自主补救

运行时将失败归类为 `access_denied`、`not_found`、`unavailable`、`timeout`、`conflict`、`verification_failed` 或 `unknown`，并在失败工具结果的 `_remediation` 中返回：

- 是否适合重试。
- 事务与回滚状态。
- 可选择的不同作用域、设备、工具或实现方向。
- 禁止原样重复无进展调用的提示。

分类只提供技术事实和候选方向。是否改用用户级安装、登录会话 Companion、另一台设备、隔离依赖或新工具，仍由模型结合目标和完整工具目录决定。

## Windows PATH

`windows.path.ensure` 是首个完整事务化 Windows 工具。默认依次尝试 Machine 与 User：

1. 捕获 Machine 和当前 User PATH。
2. 验证目标目录存在。
3. 尝试写入首选作用域并重新读取注册表。
4. 若权限拒绝或验证失败，先确认是否真的发生部分写入；只清理本次增加的目录。
5. 尝试下一作用域。
6. 更新当前服务进程 PATH，可选执行 `executable` 的真实命令解析。
7. Runtime 再次独立读取所选注册表作用域，验证后提交事务。

若后续验证失败，补偿逻辑只移除本事务拥有的 PATH 项，不覆盖其他进程并发增加的项。工具返回每个作用域的尝试、错误、清理结果和最终选择证据。

## 生成工具契约

Toolsmith 生成的 PowerShell、Python 或 Node.js 变更工具可以声明：

```json
{
  "transaction": {
    "mode": "automatic",
    "snapshot_entrypoint": "snapshot.ps1",
    "verification_entrypoint": "verify.ps1",
    "rollback_entrypoint": "rollback.ps1"
  }
}
```

平台校验三个入口都属于不可变工具包。验证入口必须返回 `verified=true`，回滚入口必须返回 `rolled_back=true`；否则事务保持失败并由恢复循环继续处理，不允许把脚本自身的成功文本当成系统状态证据。
