# BENZHI_README

基于 Go 实现的drill-seal-handover Web 项目，一款后端服务，提供地质钻孔封孔施工的任务建档、分段方案、实际施工、偏差整改、质量复验、清单冻结和不可变移交凭据工作台。

## 项目说明
- 项目：benzhi-project-4ba43f30-f6c1-46ec-8090-4368306e8373
- 项目用途：提供地质钻孔封孔施工的任务建档、分段方案、实际施工、偏差整改、质量复验、清单冻结和不可变移交凭据工作台。
- Go 工具链：`golang:1.22`
- 前端工具链：原生 HTML、CSS 和 JavaScript，由 Go 服务直接提供

## 标准构建、运行和测试命令
进入容器后执行：
cd '/app' && GOTOOLCHAIN=local go build ./...
cd /app && GOTOOLCHAIN=local go run ./cmd/drillseal -selfcheck -addr=127.0.0.1:19081
cd '/app' && GOTOOLCHAIN=local go test ./...

## Docker 构建和进入容器
chmod +x build_benzhi_docker.sh
./build_benzhi_docker.sh benzhi-project-4ba43f30-f6c1-46ec-8090-4368306e8373-amd64 linux/amd64
./build_benzhi_docker.sh benzhi-project-4ba43f30-f6c1-46ec-8090-4368306e8373-arm64 linux/arm64
docker run -it benzhi-project-4ba43f30-f6c1-46ec-8090-4368306e8373-amd64:latest

## 题目验证命令
1. 预期退出码 0：`go test ./...`
2. 预期退出码 0：`go run ./cmd/drillseal -selfcheck -addr=127.0.0.1:19081`
