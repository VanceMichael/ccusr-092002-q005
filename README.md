# 既有建筑节能改造测量档案

节能表现对比依赖用能时段、建筑使用条件和仪表校准记录,设备宣传值与实际现场读数分开保存。

本服务为现场检测团队提供完整的测量档案:建筑基线、设备更换、分时段能耗与
室内环境记录相互关联;校准人员可更正错误仪表读数且原读数保留;指标只能在
有效(validated)测量窗口和确定的口径下发布;供应商不得改写第三方结论;
业主查询报告时能看到计算使用了哪些时段、哪些读数被剔除、以及口径变化造成
的差额。领域规则详见 `docs/domain.md`。

## 运行

本服务采用 HTTP 接口,运行参数 `PORT` 指定监听端口(默认 8080),
`DATABASE_PATH` 指定数据文件(默认 `data/app.json`)。仅依赖 Go 标准库。

- `make migrate` 初始化数据文件
- `make test` 运行自动化检查
- `make run` 启动服务
- `docker compose up --build` 启动隔离容器,`APP_PORT` 调整宿主机端口

`contracts/entities.json` 记录字段与角色约定,`fixtures/example.json` 是不含
真实身份的交换示例,`migrations/` 保存等价的关系模式契约。

## 接口概览

除 `/health` 外,所有接口要求 `X-Actor-Id` 与 `X-Actor-Role`
(`field_team` / `calibrator` / `vendor` / `inspector` / `owner`)头。

| 方法 | 路径 | 角色 | 说明 |
| --- | --- | --- | --- |
| POST | `/buildings` | field_team, inspector | 登记建筑 |
| POST | `/buildings/{ref}/baselines` | field_team, inspector | 记录建筑基线 |
| POST | `/buildings/{ref}/meters` | field_team, inspector | 登记仪表 |
| POST | `/buildings/{ref}/readings` | field_team, inspector | 追加分时段读数 |
| GET | `/buildings/{ref}/readings` | 任意 | 读数视图:原值、生效值、更正链 |
| POST | `/readings/{id}/corrections` | calibrator | 更正/剔除读数,原读数保留 |
| POST | `/buildings/{ref}/equipment-replacements` | vendor, field_team, inspector | 登记设备更换 |
| POST | `/equipment-replacements/{id}/claims` | vendor | 登记宣传值(不进计算) |
| POST | `/buildings/{ref}/environment-records` | field_team, inspector | 记录室内环境 |
| POST | `/buildings/{ref}/windows` | inspector | 创建测量窗口 |
| POST | `/windows/{id}/status` | inspector | 审核窗口(validated/invalid) |
| POST | `/calibers` | inspector | 定义口径(版本自动递增) |
| POST | `/calibers/{id}/retire` | inspector | 退役口径 |
| POST | `/buildings/{ref}/reports` | inspector | 创建报告草稿(可注明 supersedes_id) |
| POST | `/reports/{id}/publish` | inspector | 校验窗口与口径后冻结发布 |
| GET | `/buildings/{ref}/reports/{id}` | 任意 | 报告全文:时段、剔除读数、更正记录 |
| GET | `/buildings/{ref}/reports/{id}/caliber-comparison` | 任意 | 各版口径重算与差额 |

列表类 GET 端点(`GET /buildings`、`GET /buildings/{ref}/...`、`GET /calibers`)
对所有已知角色开放;供应商对窗口、口径、报告、更正的写操作一律返回
`403 VENDOR_CANNOT_ALTER_CONCLUSIONS`。
