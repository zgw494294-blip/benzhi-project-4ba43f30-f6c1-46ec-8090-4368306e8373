# 钻孔封孔移交工作台

本项目用于地质钻孔封孔施工的质量验收与场地移交。现场人员可在同源浏览器工作台中建立任务、发布连续分段方案、登记实际施工、处理偏差与复验，并在满足放行规则后冻结清单、签发和校验不可变移交凭据。

数据保存在本地 SQLite 数据库中，启动时自动执行带 `schemaVersion` 的迁移并启用外键和 WAL。所有关键写操作使用任务 `expectedVersion` 进行乐观并发控制，施工、整改、复验、冻结和签发操作要求幂等键，关键动作会进入摘要审计链。

## 构建与运行

```bash
go build ./...
go run ./cmd/drillseal -addr=127.0.0.1:19081
```

浏览器打开 `http://127.0.0.1:19081/workbench`。默认数据文件为 `data/drillseal.db`，可通过 `-db` 指定其他路径。

监听地址默认为 `127.0.0.1:19081`，也可传入 `-addr=127.0.0.1:<port>`。环境变量 `PORT` 仅接受 `1024` 至 `65535` 的端口号，并绑定到 `127.0.0.1:<PORT>`；显式 `-addr` 优先于 `PORT`。

## 测试与自检

```bash
go test ./...
go run ./cmd/drillseal -selfcheck -addr=127.0.0.1:19081
```

`-selfcheck` 会使用内存数据库实际启动 HTTP 服务，通过 API 完成任务建档、方案、施工、冻结、凭据签发和摘要校验，并在限定时间内自行退出。

## 主要接口

- `GET /workbench`：浏览器工作台。
- `GET|POST /api/v1/tasks`：任务列表与建档。
- `GET /api/v1/tasks/{id}`：查询任务聚合。
- `PUT /api/v1/tasks/{id}/plan`：预览草稿相对当前方案的字段差异。
- `GET|POST /api/v1/tasks/{id}/plan`：查询只读历史快照或发布方案版本；历史查询支持 `planVersion`。
- `GET|POST /api/v1/tasks/{id}/segments`：按 `result` 筛选孔段与进度，或登记孔段施工。
- `POST|PATCH /api/v1/tasks/{id}/deviations`：提交偏差与整改。
- `POST /api/v1/tasks/{id}/reworks`：在偏差下追加返工事实与证据。
- `POST /api/v1/tasks/{id}/reviews`：质量复验。
- `GET /api/v1/tasks/{id}/preflight`：收集全部放行阻塞项，或生成冻结摘要预览。
- `POST /api/v1/tasks/{id}/freeze`：冻结移交清单。
- `GET|POST /api/v1/tasks/{id}/credential`：签发、查询或下载凭据。
- `GET /api/v1/tasks/{id}/verify`：逐项校验凭据载荷、清单摘要与审计链。

建档会统一场地空白和钻孔编号大小写，校验坐标与孔深的范围、有限值和三位小数精度，并阻止同场地同钻孔的未移交任务重复创建。任务详情包含施工进度、材料批次用量、返工事实和历次复验快照；方案有施工事实后不可修订。
