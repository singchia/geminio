GOHOSTOS?=$(shell go env GOHOSTOS)
GOARCH?=$(shell go env GOARCH)

.PHONY: all
all: traversal_internet traversal_intranet

.PHONY: clean
clean:
	rm traversal_internet traversal_intranet

traversal_internet: internet/*.go
	CGO_ENABLED=0 GOOS=$(GOHOSTOS) GOARCH=$(GOARCH) \
		go build -trimpath -ldflags "-s -w" -o traversal_internet internet/*.go

traversal_intranet: intranet/*.go
	CGO_ENABLED=0 GOOS=$(GOHOSTOS) GOARCH=$(GOARCH) \
		go build -trimpath -ldflags "-s -w" -o traversal_intranet intranet/*.go