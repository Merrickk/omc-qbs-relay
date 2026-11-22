PLUGIN_ID ?= omc-qbs-relay
VERSION ?= 0.1.0
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
PLUGIN_EXT = $(if $(filter darwin,$(GOOS)),dylib,$(if $(filter windows,$(GOOS)),dll,so))

.PHONY: test vet build clean

test:
	cd go && go test ./...

vet:
	cd go && go vet ./...

build:
	mkdir -p build
	cd go && CGO_ENABLED=1 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath -buildmode=c-shared -ldflags "-s -w -X main.pluginVersion=$(VERSION)" -o ../build/$(PLUGIN_ID).$(PLUGIN_EXT) .
	rm -f build/$(PLUGIN_ID).h

clean:
	rm -rf build
