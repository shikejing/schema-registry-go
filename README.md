# 面向低代码表单的模式注册中心（Schema Registry）

表单结构会持续演进：前后端与存量数据都需要与结构定义对齐。本注册中心把散落在各处的结构定义集中管理，支持版本化演进、兼容性校验、跨主题引用、实例校验、持久化恢复与鉴权审计。

## 特性

- **版本化管理**：按主题（topic）注册模式，版本号递增、不可变；可读取指定版本或最新版本，历史版本内容永不被改写。
- **兼容性校验**：注册前按配置执行向后兼容（BACKWARD）、向前兼容（FORWARD）或完整兼容（FULL）校验，不兼容的注册被拒绝且不改动任何已有版本。
- **并发注册**：同一主题并发注册时版本号不重复、不空洞，被拒绝的注册不产生半成品版本，最终结果与串行执行一致。
- **实例校验**：按指定版本校验实例数据，覆盖必填、类型、取值范围、枚举、字符串长度/正则、数组与嵌套结构；错误定位到字段路径并汇总全部错误。
- **跨主题引用**：模式字段可引用其他主题的模式（`topic` 或 `topic:version`）；循环引用在注册时被识别并拒绝；删除仍被引用的版本被拒绝。
- **持久化与重启恢复**：已提交版本、兼容性配置与审计记录原子落盘，重启后完整还原，未完成注册的中间状态不残留。
- **鉴权与审计**：基于 API Key 的角色鉴权（reader/writer/admin），越权调用被拒绝；所有变更可追溯到调用方与时间。

## 模式定义

模式为 JSON 结构，字段类型支持 `object`、`string`、`integer`、`number`、`boolean`、`array`：

```json
{
  "type": "object",
  "fields": [
    { "name": "name", "type": "string", "required": true, "minLength": 2, "maxLength": 50 },
    { "name": "age", "type": "integer", "minimum": 0, "maximum": 150 },
    { "name": "gender", "type": "string", "enum": ["male", "female"] },
    { "name": "tags", "type": "array", "items": { "name": "_", "type": "string" } },
    { "name": "contact", "type": "object", "ref": "contact" }
  ]
}
```

字段约束：

| 约束 | 适用类型 | 说明 |
| --- | --- | --- |
| `required` | object 字段 | 是否必填 |
| `enum` | string/integer/number/boolean | 枚举值 |
| `minimum` / `maximum` | integer/number | 数值闭区间 |
| `minLength` / `maxLength` | string/array | 字符串长度 / 数组元素个数 |
| `pattern` | string | 正则表达式（任何变更均判定为不兼容，见下） |
| `ref` | 任意 | 跨主题引用，形如 `topic`（最新版）或 `topic:2`（指定版本） |

## 兼容性语义

以「新模式（待注册） vs 旧模式（最新版本）」判定：

| 规则 | BACKWARD 向后兼容 | FORWARD 向前兼容 | FULL 完整兼容 |
| --- | --- | --- | --- |
| 新增字段 | 可选字段允许；**必填字段拒绝** | 允许 | 拒绝 |
| 删除字段 | 允许 | **必填字段拒绝**；可选字段允许 | 拒绝 |
| 字段由可选变必填 | 拒绝 | 允许 | 拒绝 |
| 字段由必填变可选 | 允许 | 拒绝 | 拒绝 |
| 修改字段类型 | 拒绝 | 拒绝 | 拒绝 |
| 枚举 | 新枚举须**包含**旧枚举全部值（不得移除枚举值） | 新枚举须为旧枚举**子集**（不得新增枚举值） | 两边规则同时满足 |
| 数值范围 | 新范围须为旧范围**超集**（不得收紧） | 新范围须为旧范围**子集**（不得放宽） | 两边规则同时满足 |
| 字符串/数组长度 | 不得收紧 | 不得放宽 | 两边规则同时满足 |
| pattern | 任何差异均拒绝（保守策略：无法静态证明存量数据仍满足新正则） | 同左 | 同左 |

- **BACKWARD**：新模式的消费者可以读取旧模式产生的数据。
- **FORWARD**：旧模式的消费者可以读取新模式产生的数据。
- **FULL**：同时满足两者。
- **NONE**：不做兼容性检查。
- 默认级别为 **BACKWARD**，可按主题通过接口配置。

## 配置项

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `ADDR` | `:8080` | HTTP 服务监听地址 |
| `DATA_PATH` | `data/registry.json` | 持久化文件路径 |

内置 API Key（生产环境请替换）：

| API Key | 角色 | 权限 |
| --- | --- | --- |
| `admin123` | admin | 读、写、删除、兼容性配置、审计查询 |
| `writer123` | writer | 读、注册新模式、兼容性预校验 |
| `reader123` | reader | 读取、实例校验 |

## HTTP API

所有接口通过 `X-API-Key` 头进行鉴权。

| 方法 | 路径 | 角色 | 说明 |
| --- | --- | --- | --- |
| GET | `/health` | 公开 | 健康检查 |
| GET | `/topics` | reader+ | 列出全部主题 |
| GET | `/topics/{topic}` | reader+ | 主题信息（版本列表 + 兼容性级别） |
| GET | `/topics/{topic}/versions` | reader+ | 列出主题的版本号 |
| POST | `/topics/{topic}/versions` | writer+ | 注册新模式版本 |
| GET | `/topics/{topic}/versions/{version}` | reader+ | 读取指定版本；`version` 为 `latest` 表示最新版 |
| DELETE | `/topics/{topic}/versions/{version}` | admin | 删除指定版本（仍被引用时拒绝） |
| POST | `/topics/{topic}/validate?version=X` | reader+ | 校验实例数据（`version` 缺省取最新版） |
| POST | `/topics/{topic}/compatibility` | writer+ | 对待注册模式做兼容性预校验（不写入） |
| GET | `/topics/{topic}/compatibility` | reader+ | 获取主题兼容性级别 |
| PUT | `/topics/{topic}/compatibility` | admin | 设置主题兼容性级别 |
| GET | `/audit` | admin | 查询审计记录 |

### 示例

注册模式：

```bash
curl -X POST http://localhost:8080/topics/form/versions \
  -H "X-API-Key: writer123" -H "Content-Type: application/json" \
  -d '{"type":"object","fields":[{"name":"name","type":"string","required":true}]}'
```

校验实例（错误汇总返回）：

```bash
curl -X POST "http://localhost:8080/topics/form/validate" \
  -H "X-API-Key: reader123" -H "Content-Type: application/json" \
  -d '{"age":200}'
```

设置完整兼容：

```bash
curl -X PUT http://localhost:8080/topics/form/compatibility \
  -H "X-API-Key: admin123" -H "Content-Type: application/json" \
  -d '{"compatibility":"FULL"}'
```

## 持久化与恢复

- 全部状态（版本、兼容性配置、审计记录）保存在单个 JSON 文件中。
- 写入采用「临时文件 + 原子 rename」：任何时刻崩溃，文件要么是旧的完整状态，要么是新的完整状态，不存在半成品。
- 注册/删除/配置变更均先持久化成功后才提交内存状态；重启后从文件完整还原。

## 构建与测试

```bash
go build ./...
go test ./...
```

## 项目结构

```
.
├── main.go                     # HTTP 服务入口与路由
├── main_test.go                # HTTP 接口测试
└── internal/
    ├── schema/                 # 模式定义、实例校验、兼容性校验
    │   ├── schema.go
    │   ├── validate.go
    │   ├── validate_test.go
    │   ├── compat.go
    │   └── compat_test.go
    ├── registry/               # 注册中心：版本管理、并发控制、引用/循环、删除保护
    │   ├── registry.go
    │   └── registry_test.go
    ├── store/                  # 持久化（原子写入 + 恢复）
    │   ├── store.go
    │   └── store_test.go
    └── auth/                   # 鉴权与审计
        └── auth.go
```
