# MongoDB JSON Formatter

一个纯前端的 `MongoDB JSON` 格式化与对比工具，适合处理包含 `ObjectId`、`ISODate`、`NumberInt`、`NumberLong` 等 Mongo shell 风格数据的内容。

## 功能特性

- `MongoDB JSON` 格式化与高亮展示
- 普通模式下的 `input -> output` 格式化流程
- 输出内容复制、编辑、收起/展开
- 深色 / 浅色主题切换
- 对比模式下左右双栏编辑
- 对比模式中间行号显示
- 对比前字段排序，减少仅因字段顺序不同造成的干扰
- 字段级差异高亮，能直观看到新增字段和缺失字段
- **📊 Mongosql 表格视图**：将 MongoDB 文档展平为 SQL 风格表格
  - 嵌套字段以点号路径展示（如 `benefit.benefitName`）
  - 字段类型推断与类型徽章（string / number / date / bool / oid / mixed）
  - 结构校验报告：全 null 列、类型不一致、缺失率>50% 等
  - 列排序（点击表头）、全文搜索筛选、分页浏览
  - 一键导出 CSV（BOM 头，兼容 Excel）
- **🔧 Shell 语句格式化**：对 MongoDB Shell 命令（如 `db.collection.updateOne(...)`）进行智能格式化
  - 语法高亮：`db`、方法名、`$` 操作符、字符串、注释
  - 自动缩进 JSON 参数
  - 结构校验：未知方法名、未定义操作符、括号匹配检查

## 快速开始

直接打开 `index.html` 即可使用。

如果希望通过本地服务访问，可以执行：

```bash
python3 -m http.server 8000
```

然后在浏览器打开：

```text
http://localhost:8000/
```

## 使用说明

### 普通格式化模式

- 在左侧 `INPUT` 输入或粘贴 MongoDB JSON
- 点击顶部 `✨ 格式化` 执行格式化
- 右侧 `OUTPUT` 展示格式化结果
- 点击顶部 `格式化` 按钮可切回普通格式化页面

### 对比模式

- 点击顶部 `🔍 对比` 进入对比模式
- 左右两侧都可直接编辑
- 点击顶部 `✨ 格式化` 会先按字段排序，再刷新差异结果
- 中间显示同步滚动的双列行号
- 缺失字段与新增字段会在正文中高亮显示

## 技术说明

- 单文件实现：`index.html`
- 使用原生 HTML、CSS、JavaScript
- 对比编辑器基于 Monaco Diff Editor
- Monaco 资源通过 CDN 加载

## 文件结构

```text
.
├── index.html
└── README.md
```

## 适用场景

- 查看 Mongo shell 风格对象
- 对比两份 MongoDB 文档差异
- 排查字段新增、缺失、顺序变化

