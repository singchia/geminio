GOHOSTOS:=$(shell go env GOHOSTOS)
GOARCH:=$(shell go env GOARCH)

.PHONY: all
all: mc ms

.PHONY: clean
clean:
	rm mc ms

mc: client/*.go share/*.go
	CGO_ENABLED=0 GOOS=$(GOHOSTOS) GOARCH=$(GOARCH) \
		go build -trimpath -ldflags "-s -w" -o mc client/messager_client.go

ms: server/*.go share/*.go
	CGO_ENABLED=0 GOOS=$(GOHOSTOS) GOARCH=$(GOARCH) \
		go build -trimpath -ldflags "-s -w" -o ms server/messager_server.go