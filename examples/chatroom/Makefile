GOHOSTOS?=$(shell go env GOHOSTOS)
GOARCH?=$(shell go env GOARCH)

.PHONY: all
all: chatroom_server chatroom_client

.PHONY: clean
clean:
	rm chatroom_server chatroom_client

chatroom_server: server/*.go share/*.go
	CGO_ENABLED=0 GOOS=$(GOHOSTOS) GOARCH=$(GOARCH) \
		go build -trimpath -ldflags "-s -w" -o chatroom_server server/*.go

chatroom_client: client/*.go share/*.go
	CGO_ENABLED=0 GOOS=$(GOHOSTOS) GOARCH=$(GOARCH) \
		go build -trimpath -ldflags "-s -w" -o chatroom_client client/*.go