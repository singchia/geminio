GOHOSTOS?=$(shell go env GOHOSTOS)
GOARCH?=$(shell go env GOARCH)

.PHONY: all
all: consumer producer broker

.PHONY: clean
clean:
	rm consumer producer broker

consumer: consumer/*.go
	CGO_ENABLED=0 GOOS=$(GOHOSTOS) GOARCH=$(GOARCH) \
		go build -trimpath -ldflags "-s -w" -o consumer consumer/consumer.go

producer: producer/*.go
	CGO_ENABLED=0 GOOS=$(GOHOSTOS) GOARCH=$(GOARCH) \
		go build -trimpath -ldflags "-s -w" -o producer producer/producer.go

broker: broker/*.go
	CGO_ENABLED=0 GOOS=$(GOHOSTOS) GOARCH=$(GOARCH) \
		go build -trimpath -ldflags "-s -w" -o broker broker/main.go