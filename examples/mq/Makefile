GOHOSTOS?=$(shell go env GOHOSTOS)
GOARCH?=$(shell go env GOARCH)

.PHONY: all
all: mq_consumer mq_producer mq_broker

.PHONY: clean
clean:
	rm mq_consumer mq_producer mq_broker

mq_consumer: consumer/*.go
	CGO_ENABLED=0 GOOS=$(GOHOSTOS) GOARCH=$(GOARCH) \
		go build -trimpath -ldflags "-s -w" -o mq_consumer consumer/consumer.go

mq_producer: producer/*.go
	CGO_ENABLED=0 GOOS=$(GOHOSTOS) GOARCH=$(GOARCH) \
		go build -trimpath -ldflags "-s -w" -o mq_producer producer/producer.go

mq_broker: broker/*.go
	CGO_ENABLED=0 GOOS=$(GOHOSTOS) GOARCH=$(GOARCH) \
		go build -trimpath -ldflags "-s -w" -o mq_broker broker/*.go