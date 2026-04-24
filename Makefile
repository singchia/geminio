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