GOHOSTOS?=$(shell go env GOHOSTOS)
GOARCH?=$(shell go env GOARCH)

.PHONY: all
all: packets_drop/packets_drop

.PHONY: clean
clean:
	rm packets_drop/packets_drop

.PHONY: packets_drop

packets_drop/packets_drop: packets_drop/*.go
	CGO_ENABLED=0 GOOS=$(GOHOSTOS) GOARCH=$(GOARCH) \
		go build -trimpath -ldflags "-s -w" -o packets_drop/packets_drop packets_drop/main.go
