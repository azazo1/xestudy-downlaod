[private]
default:
    @just --list

# 拉取依赖.
deps:
    go mod download

# 整理依赖.
tidy:
    go mod tidy

# 构建二进制到 bin/xestudy-downlaod. (dev-build, 不注入版本号)
build:
    go build -o bin/xestudy-downlaod ./cmd/xestudy-downlaod

# 根据当前平台生成发布产物到 dist/, 版本号来自 git tag.
dist:
    uv run --no-project python scripts/dist.py

# just serve --concurrent 8
# 前台启动下载服务, 视频保存到 ./videos, 额外参数透传给程序.
serve *args:
    go run ./cmd/xestudy-downlaod --savepath ./videos {{args}}

# 运行全部单元测试, 测试不访问外网.
test:
    go test ./... -count=1

# 端到端自检: 起一个假视频源, 提交一次下载任务并校验成片.
smoke:
    uv run --no-project python scripts/smoke-test.py

# 检查 reqable 抓包脚本的入口与提交行为, 不需要安装 reqable.
check-reqable:
    uv run --no-project python scripts/check-reqable-script.py

# 静态检查.
vet:
    go vet ./...

# 代码格式化.
fmt:
    gofmt -w .
