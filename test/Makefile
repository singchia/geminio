.PHONY: all unit integration e2e regression chaos security harness

all: unit integration regression e2e security harness chaos

unit:
	cd unit && go test -count=1 -timeout=120s ./

integration:
	cd integration && go test -count=1 -timeout=300s ./

regression:
	cd regression && go test -count=1 -timeout=120s ./

e2e:
	cd e2e && go test -count=1 -timeout=300s ./

security:
	cd security && go test -count=1 -timeout=300s ./

harness:
	cd harness && go test -count=1 -timeout=60s ./

chaos:
	cd chaos && go test -run "^Test[^P]" -count=1 -timeout=600s ./