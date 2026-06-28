.PHONY: build build-go build-go-api web-setup web-build web-dev test run fmt tidy

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

# 完整 Windows 版：嵌入 ffmpeg + 前端
build-windows-amd64:
	GOOS=windows GOARCH=amd64 go build -tags embed_ffmpeg,embedded_web -o hikami-windows-amd64.exe ./cmd/hikami

# 轻量 Windows 版：嵌入前端，依赖系统 ffmpeg（不嵌 ffmpeg）
build-windows-amd64-lite:
	GOOS=windows GOARCH=amd64 go build -tags embedded_web -o hikami-windows-amd64-lite.exe ./cmd/hikami
