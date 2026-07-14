.PHONY: build build-go build-go-api web-setup web-build web-dev test run fmt tidy api-docs api-lint api-gen-types \
        build-ffmpeg-minimal verify-ffmpeg-minimal

# 完整构建：先产出前端 webdist，再以 embedded_web 标签嵌入编译（ISS-1）
build: web-build build-go

# 嵌入前端构建（需 cmd/hikami/webdist 存在，由 web-build 产出）
build-go:
	go build -tags embedded_web -o ./hikami ./cmd/hikami
	@strings ./hikami | grep -q 'webdist/' || (echo "ERROR: frontend not embedded in ./hikami (build tag missing?)" && exit 1)

# 纯 API 构建：不嵌入前端，无需 webdist，可直接编译/测试（ISS-1）
build-go-api:
	go build -o ./hikami-api ./cmd/hikami

web-setup:
	cd web && npm install

web-dev:
	cd web && npm run dev

web-build: web-setup
	cd web && npm run build
	rm -rf cmd/hikami/webdist
	cp -r web/dist cmd/hikami/webdist

test:
	go test ./...

run:
	go run -tags embedded_web ./cmd/hikami -config config.yaml

fmt:
	gofmt -w cmd internal

tidy:
	go mod tidy

# 交叉编译（含前端嵌入，需先 make web-build 产出 cmd/hikami/webdist）
build-linux-amd64:
	GOOS=linux GOARCH=amd64 go build -tags embedded_web -o hikami-linux-amd64 ./cmd/hikami

build-linux-arm64:
	GOOS=linux GOARCH=arm64 go build -tags embedded_web -o hikami-linux-arm64 ./cmd/hikami

build-darwin-arm64:
	GOOS=darwin GOARCH=arm64 go build -tags embedded_web -o hikami-darwin-arm64 ./cmd/hikami

# Windows 单文件版：嵌入裁剪版 ffmpeg + 前端。裁剪版仅含本项目用到的音频
# demuxer/muxer(flv/concat/mov/mp3) + mp3/aac encoder（约 8-12MB），由
# build-ffmpeg-minimal 产出。需先 make build-ffmpeg-minimal 生成 assets/ffmpeg.zip。
build-windows-amd64:
	GOOS=windows GOARCH=amd64 go build -tags embed_ffmpeg,embedded_web -o hikami-windows-amd64.exe ./cmd/hikami
	@ls -lh hikami-windows-amd64.exe | awk '{print "  产物体积:", $5}'
	@echo "  注：嵌入的是裁剪版 ffmpeg，若 assets/ffmpeg.zip 缺失会编译失败，先跑 make build-ffmpeg-minimal"

# 轻量 Windows 版：嵌入前端，依赖系统 ffmpeg（不嵌 ffmpeg，体积最小）
build-windows-amd64-lite:
	GOOS=windows GOARCH=amd64 go build -tags embedded_web -o hikami-windows-amd64-lite.exe ./cmd/hikami

# Windows 桌面版：嵌入裁剪版 ffmpeg + 前端 + 系统托盘（隐藏终端窗口）
# 加 -H windowsgui 让 exe 不弹控制台，加 systray tag 编译托盘代码
build-windows-desktop:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -tags 'embed_ffmpeg,embedded_web,systray' \
		-ldflags='-H windowsgui -s -w' -o hikami-windows-desktop.exe ./cmd/hikami
	@ls -lh hikami-windows-desktop.exe | awk '{print "  产物体积:", $5}'

# Windows 桌面轻量版：嵌入前端 + 系统托盘，依赖系统 ffmpeg
build-windows-desktop-lite:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -tags 'embedded_web,systray' \
		-ldflags='-H windowsgui -s -w' -o hikami-windows-desktop-lite.exe ./cmd/hikami

# 构建裁剪版 ffmpeg/ffprobe（Windows x64 静态），产出 internal/runtime/assets/ffmpeg.zip。
# 仅在更新嵌入的 ffmpeg 时运行（Docker 交叉编译，约 5-10 分钟）。详见 scripts/README-ffmpeg-build.md。
build-ffmpeg-minimal:
	./scripts/build-ffmpeg-minimal.sh

# 验证裁剪版 ffmpeg.zip 覆盖本项目全部调用路径（6 条用例）。在 Windows 上跑。
verify-ffmpeg-minimal:
	./scripts/verify-ffmpeg-minimal.sh

# API 文档渲染（本地静态服务器）
api-docs:
	@echo "API 文档: http://127.0.0.1:6335"
	@cd docs/api && python3 -m http.server 6335

# 校验 openapi.yaml 语法（首次会下载 @redocly/cli）
api-lint:
	@npx -y @redocly/cli lint docs/api/openapi.yaml

# 从 OpenAPI 生成 TS 类型（前端重写阶段，首次会下载 openapi-typescript）
api-gen-types:
	@npx -y openapi-typescript docs/api/openapi.yaml -o web/src/api/generated.ts
