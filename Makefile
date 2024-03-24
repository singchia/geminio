.PHONY: test
test:
	make -C test

.PHONY: examples
examples:
	make -C examples

.PHONY: clean
clean:
	make clean -C examples