.PHONY: test
test:
	make -C test

.PHONY: bench
bench:
	go test -run '^$$' -bench . -benchtime 3s -benchmem -timeout 600s ./test/bench/...

.PHONY: examples
examples:
	make -C examples

.PHONY: clean
clean:
	make clean -C examples
.PHONY: test-race test-short-race vet
# Full suite, including long-running chaos tests. Override for slower CI hosts.
TEST_TIMEOUT ?= 600s
test-race:
	go test -race ./... -count=1 -timeout=$(TEST_TIMEOUT)

test-short-race:
	go test -race ./... -short -count=1 -timeout=$(TEST_TIMEOUT)

vet:
	go vet ./...
