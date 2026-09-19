# 既有建筑节能改造测量档案

面向高碑店绿色建筑企业现场检测团队的测量档案服务：把**建筑基线、设备更换、分时段能耗读数、室内环境记录**关联到同一条证据链上，支撑新建项目与老旧住宅改造的节能效果对比。

核心原则：

- **读数不可变**：现场读数终身保留；仪表错误由校准人员追加更正，原读数与更正原因在报告中同时可见。
- **指标闸门发布**：只有 finalized 测量窗口 + locked 口径版本，且覆盖度/采暖季/占用率条件满足时才产出数字，否则返回 422 阻断项。
- **宣传值隔离**：供应商展会/手册数值单独存为 `marketing_claim*`，标注来源，绝不进入节能计算。
- **第三方结论不可改写**：结论仅第三方角色可追加，无修改、删除路径，供应商不持有任何系统令牌。
- **报告可追溯**：每份报告固化时段、参与读数（含原值/更正值）、被剔除读数及原因、口径版本、口径变化差额。
- **哈希链存储**：运行时为仅追加日志，每行含前序哈希与载荷哈希；篡改任何历史行都会在校验中暴露。

## 角色与令牌

| 角色 | 环境变量 | 主要权限 |
|---|---|---|
| 现场检测团队 | `FIELD_TOKEN` | 全部档案、窗口、口径、报告发布 |
| 校准人员 | `CALIBRATION_TOKEN` | 仅对读数追加更正 |
| 第三方机构 | `THIRD_PARTY_TOKEN` | 对报告追加结论 |
| 业主 | `OWNER_TOKEN` | 只读：查询报告与链校验，不能写入 |
| 供应商 | 无 | 无法登录、无法改数、无法改结论 |

未设置环境变量时使用开发用默认令牌（`dev-field-token` 等），生产必须注入。

## 接口（均为 `/v1` 前缀，Bearer 鉴权）

```
POST   /buildings                         建立建筑基线
POST   /projects                          新建/改造项目
POST   /equipment                         设备更换（rated_* 与 marketing_claim_* 分栏）
POST   /meters                            登记仪表
POST   /readings                          写入不可变读数
GET    /readings/{id}                     查询读数及其更正（如有）
POST   /readings/{id}/corrections         校准人员追加更正（仅 calibration）
POST   /indoor                            分时段室内环境/占用率
POST   /methodologies                     建立口径新版本（draft）
POST   /methodologies/{id}/lock           锁定口径（旧锁定版本自动 superseded）
POST   /windows                           建立测量窗口（draft，绑定口径）
POST   /windows/{id}/exclusions           剔除读数（必须带原因）
POST   /windows/{id}/finalize             终结窗口（口径须 locked）
POST   /reports                           发布节能报告（闸门不通过返回 422 + blockers）
GET    /reports[?project_id=]             报告列表
GET    /reports/{id}                      报告全文（含可追溯快照与第三方结论）
POST   /reports/{id}/retract              撤回报告（只追加，记录保留）
POST   /reports/{id}/conclusions          第三方追加结论（仅 third_party）
GET    /audit/verify                      哈希链完整性校验
GET    /audit/chain                       事件链浏览（审计）
GET    /health                            健康检查
```

时间字段统一为带时区偏移的 ISO 8601；数值字段一律十进制字符串。字段与枚举约定见 `contracts/entities.json`，规范关系模式与不可变触发器见 `migrations/001_bootstrap.sql`，领域说明见 `docs/domain.md`。

## 本地开发

`make migrate` 确保数据目录存在；`make test` 运行自动化检查；`make run` 启动服务（默认 `DATABASE_PATH=data/app.log`，服务启动即创建并校验日志）。`docker compose up --build` 启动隔离容器，`APP_PORT` 调整宿主机端口。
