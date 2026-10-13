APP     := crontabui
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
GOFLAGS := -trimpath

.PHONY: all build linux-amd64 linux-386 linux-arm64 linux-arm windows test vet fmt tidy clean size

all: linux-amd64

build: linux-amd64

# 生产目标：Linux x86_64 静态二进制（无 CGO，可在任意发行版直接运行）
linux-amd64:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/$(APP)-linux-amd64 ./cmd/$(APP)
	@cp -f deploy/install-corntab.sh dist/install-corntab.sh
	@echo "构建完成：dist/$(APP)-linux-amd64 + dist/install-corntab.sh"

# 32 位 x86
linux-386:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=386 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/$(APP)-linux-386 ./cmd/$(APP)
	@cp -f deploy/install-corntab.sh dist/install-corntab.sh
	@echo "构建完成：dist/$(APP)-linux-386 + dist/install-corntab.sh"

linux-arm64:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/$(APP)-linux-arm64 ./cmd/$(APP)
	@cp -f deploy/install-corntab.sh dist/install-corntab.sh
	@echo "构建完成：dist/$(APP)-linux-arm64 + dist/install-corntab.sh"

# 32 位 ARM，硬浮点 ARMv7（树莓派 2/Zero 2 等）。产物名带 v7 后缀以区别 arm64
linux-arm:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/$(APP)-linux-armv7 ./cmd/$(APP)
	@cp -f deploy/install-corntab.sh dist/install-corntab.sh
	@echo "构建完成：dist/$(APP)-linux-armv7 + dist/install-corntab.sh"

windows:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/$(APP)-windows-amd64.exe ./cmd/$(APP)
	@echo "构建完成：dist/$(APP)-windows-amd64.exe"

test:
	go test -count=1 ./...

vet:
	go vet ./...

fmt:
	gofmt -s -w .

tidy:
	go mod tidy

size: linux-amd64
	@ls -lh dist/$(APP)-linux-amd64

clean:
	rm -rf dist
