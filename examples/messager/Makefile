GOHOSTOS?=$(shell go env GOHOSTOS)
GOARCH?=$(shell go env GOARCH)

.PHONY: all
all: message_client message_server

.PHONY: clean
clean:
	rm message_client message_server

message_client: client/*.go share/*.go
	CGO_ENABLED=0 GOOS=$(GOHOSTOS) GOARCH=$(GOARCH) \
		go build -trimpath -ldflags "-s -w" -o message_client client/messager_client.go

message_server: server/*.go share/*.go
	CGO_ENABLED=0 GOOS=$(GOHOSTOS) GOARCH=$(GOARCH) \
		go build -trimpath -ldflags "-s -w" -o message_server server/messager_server.go