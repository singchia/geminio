GOHOSTOS?=$(shell go env GOHOSTOS)
GOARCH?=$(shell go env GOARCH)

.PHONY: all
all: chatroom messager mq relay traversal

.PHONY: chatroom
chatroom:
	make -C chatroom

.PHONY: messager
messager:
	make -C messager

.PHONY: mq
mq:
	make -C mq

.PHONY: relay
relay:
	make -C relay

.PHONY: traversal
traversal:
	make -C traversal

.PHONY: clean
clean:
	make clean -C chatroom
	make clean -C messager
	make clean -C mq
	make clean -C relay
	make clean -C traversal
