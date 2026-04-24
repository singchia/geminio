#!/bin/bash

# Gemino Test Suite Runner — see `test/run_tests.sh -h`

# Benchmark -benchtime defaults (longer runs: BENCH_TIME_EACH=5s BENCH_TIME_ALL=30s ./test/run_tests.sh --bench)
: "${BENCH_TIME_EACH:=3s}"
: "${BENCH_TIME_ALL:=10s}"
# go test -timeout: applies to the whole process each invocation (default without this: ~10m, too short for bench=. + long benchtime). Use 0 to disable.
: "${BENCH_GO_TEST_TIMEOUT:=45m}"

OUTPUT_FILE=""
run_unit=false
run_bench=false
run_e2e=false
run_security=false
run_fuzz=false
run_race=false
run_cover=false
any_category=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        -o|--output)
            if [[ -z "${2:-}" ]]; then
                echo "Error: $1 requires a file path" >&2
                exit 1
            fi
            OUTPUT_FILE="$2"
            shift 2
            ;;
        -h|--help)
            cat <<'EOF'
Usage: test/run_tests.sh [options] [--category ...]

Options:
  -o, --output FILE   Write full output to FILE (overwrite; also shown on terminal)
  -h, --help          Show this help

Categories (combine multiple; omit all to run everything):
  --unit       Unit tests: go test ./... -short
  --bench      Benchmarks under test/bench
  --e2e        End-to-end tests under test/e2e
  --security   Security tests under test/security
  --fuzz       Native fuzz targets (Go 1.18+)
  --race       Race detector on ./... -short
  --cover      Coverage (coverage.out, coverage.html)
  --all        Explicitly run all categories

Examples:
  test/run_tests.sh --unit
  test/run_tests.sh --e2e --security
  test/run_tests.sh -o run.log --bench

Env (benchmark section only):
  BENCH_TIME_EACH        Per-group -benchtime (default: 3s)
  BENCH_TIME_ALL         Final bench=. -benchtime (default: 10s)
  BENCH_GO_TEST_TIMEOUT  go test -timeout for each bench invocation (default: 45m; 0 = no limit)

Note: BENCH_TIME_ALL applies to every benchmark matched by bench=. (including sub-benchmarks);
  wall clock grows with count × benchtime. Raising BENCH_TIME_ALL without raising
  BENCH_GO_TEST_TIMEOUT will hit Go's default ~10m and get "ran too long".
EOF
            exit 0
            ;;
        --unit)
            run_unit=true
            any_category=true
            shift
            ;;
        --bench)
            run_bench=true
            any_category=true
            shift
            ;;
        --e2e)
            run_e2e=true
            any_category=true
            shift
            ;;
        --security)
            run_security=true
            any_category=true
            shift
            ;;
        --fuzz)
            run_fuzz=true
            any_category=true
            shift
            ;;
        --race)
            run_race=true
            any_category=true
            shift
            ;;
        --cover|--coverage)
            run_cover=true
            any_category=true
            shift
            ;;
        --all)
            run_unit=true
            run_bench=true
            run_e2e=true
            run_security=true
            run_fuzz=true
            run_race=true
            run_cover=true
            any_category=true
            shift
            ;;
        *)
            echo "Unknown option: $1 (try -h)" >&2
            exit 1
            ;;
    esac
done

if ! $any_category; then
    run_unit=true
    run_bench=true
    run_e2e=true
    run_security=true
    run_fuzz=true
    run_race=true
    run_cover=true
fi

# Resolve relative log path to invocation cwd (before cd to project root)
if [[ -n "$OUTPUT_FILE" ]]; then
    if [[ "$OUTPUT_FILE" != /* ]]; then
        OUTPUT_FILE="$(pwd)/$OUTPUT_FILE"
    fi
    mkdir -p "$(dirname "$OUTPUT_FILE")"
    exec > >(tee "$OUTPUT_FILE") 2>&1
fi

set -e

echo "==================================="
echo "Gemino Comprehensive Test Suite"
echo "==================================="
if [[ -n "$OUTPUT_FILE" ]]; then
    echo "Full output also logged to: $OUTPUT_FILE"
fi
echo "Categories:"
$run_unit       && echo "  - unit"
$run_bench      && echo "  - bench"
$run_e2e        && echo "  - e2e"
$run_security   && echo "  - security"
$run_fuzz       && echo "  - fuzz"
$run_race       && echo "  - race"
$run_cover      && echo "  - cover"
echo ""

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Function to print section headers
print_header() {
    echo ""
    echo -e "${YELLOW}===================================${NC}"
    echo -e "${YELLOW}$1${NC}"
    echo -e "${YELLOW}===================================${NC}"
    echo ""
}

# Function to run tests with timeout
run_test_with_timeout() {
    local timeout=$1
    shift
    timeout "$timeout" go test -v "$@" || true
}

# Change to project root
cd "$(dirname "$0")/.."

# Install dependencies
echo "Installing dependencies..."
go mod download

if $run_unit; then
    print_header "Running Unit Tests"
    go test -v ./... -short -count=1 2>&1 | head -100 || true
fi

if $run_bench; then
    print_header "BENCHMARK TESTS"

    echo "Running Message Benchmarks..."
    go test -timeout="${BENCH_GO_TEST_TIMEOUT}" -bench=BenchmarkMessage -benchmem -benchtime="${BENCH_TIME_EACH}" ./test/bench/... 2>&1 || true

    echo ""
    echo "Running RPC Benchmarks..."
    go test -timeout="${BENCH_GO_TEST_TIMEOUT}" -bench=BenchmarkRPC -benchmem -benchtime="${BENCH_TIME_EACH}" ./test/bench/... 2>&1 || true

    echo ""
    echo "Running Stream Benchmarks..."
    go test -timeout="${BENCH_GO_TEST_TIMEOUT}" -bench=BenchmarkStream -benchmem -benchtime="${BENCH_TIME_EACH}" ./test/bench/... 2>&1 || true

    echo ""
    echo "Running End Benchmarks..."
    go test -timeout="${BENCH_GO_TEST_TIMEOUT}" -bench=BenchmarkEnd -benchmem -benchtime="${BENCH_TIME_EACH}" ./test/bench/... 2>&1 || true

    echo ""
    echo "Running Connection Benchmarks..."
    go test -timeout="${BENCH_GO_TEST_TIMEOUT}" -bench=BenchmarkConnection -benchmem -benchtime="${BENCH_TIME_EACH}" ./test/bench/... 2>&1 || true

    echo ""
    echo "Running Memory Benchmarks..."
    go test -timeout="${BENCH_GO_TEST_TIMEOUT}" -bench=BenchmarkMemory -benchmem -benchtime="${BENCH_TIME_EACH}" ./test/bench/... 2>&1 || true

    print_header "Running All Benchmarks (${BENCH_TIME_ALL} per package)..."
    go test -timeout="${BENCH_GO_TEST_TIMEOUT}" -bench=. -benchmem -benchtime="${BENCH_TIME_ALL}" ./test/bench/... 2>&1 || true
fi

if $run_e2e; then
    print_header "E2E INTEGRATION TESTS"

    echo "Running Connection Tests..."
    go test -v -run TestConnection ./test/e2e/... -count=1 2>&1 | head -50 || true

    echo ""
    echo "Running Message Tests..."
    go test -v -run TestMessage ./test/e2e/... -count=1 2>&1 | head -100 || true

    echo ""
    echo "Running RPC Tests..."
    go test -v -run TestRPC ./test/e2e/... -count=1 2>&1 | head -100 || true

    echo ""
    echo "Running Stream Tests..."
    go test -v -run TestStream ./test/e2e/... -count=1 2>&1 | head -100 || true

    echo ""
    echo "Running Error Recovery Tests..."
    go test -v -run TestConnectionRecon ./test/e2e/... -count=1 2>&1 || true

    echo ""
    echo "Running Resource Cleanup Tests..."
    go test -v -run TestResourceCleanup ./test/e2e/... -count=1 2>&1 || true

    echo ""
    echo "Running Stress Tests (this may take a while)..."
    go test -v -run TestStress ./test/e2e/... -count=1 -timeout=10m 2>&1 || true

    print_header "Running All E2E Tests..."
    go test -v ./test/e2e/... -count=1 -timeout=15m 2>&1 || true
fi

if $run_security; then
    print_header "SECURITY TESTS"

    echo "Running Input Validation Tests..."
    go test -v -run TestLargePayload ./test/security/... -count=1 2>&1 || true
    go test -v -run TestEmptyPayload ./test/security/... -count=1 2>&1 || true
    go test -v -run TestNilData ./test/security/... -count=1 2>&1 || true
    go test -v -run TestSpecialCharacters ./test/security/... -count=1 2>&1 || true

    echo ""
    echo "Running Boundary Tests..."
    go test -v -run TestBoundary ./test/security/... -count=1 2>&1 || true

    echo ""
    echo "Running DoS Protection Tests..."
    go test -v -run TestDoS ./test/security/... -count=1 -timeout=5m 2>&1 || true

    echo ""
    echo "Running Fuzzing Tests..."
    go test -v -run TestFuzz ./test/security/... -count=1 2>&1 || true

    echo ""
    echo "Running Injection Tests..."
    go test -v -run TestSQLInjection ./test/security/... -count=1 2>&1 || true
    go test -v -run TestCommandInjection ./test/security/... -count=1 2>&1 || true
    go test -v -run TestPathTraversal ./test/security/... -count=1 2>&1 || true

    echo ""
    echo "Running Race Condition Tests..."
    go test -v -race -run TestRace ./test/security/... -count=1 2>&1 || true

    echo ""
    echo "Running Resource Exhaustion Tests..."
    go test -v -run TestResourceExhaustion ./test/security/... -count=1 -timeout=5m 2>&1 || true

    echo ""
    echo "Running Timing Attack Tests..."
    go test -v -run TestTiming ./test/security/... -count=1 2>&1 || true

    print_header "Running All Security Tests..."
    go test -v ./test/security/... -count=1 -timeout=15m 2>&1 || true
fi

if $run_fuzz; then
    print_header "FUZZING TESTS"

    if go version | grep -qE 'go1\.(1[89]|2[0-9]|[3-9][0-9])'; then
        echo "Running FuzzRPCData (30 seconds)..."
        go test -fuzz=FuzzRPCData -fuzztime=30s ./test/security/... 2>&1 || true

        echo ""
        echo "Running FuzzMessageData (30 seconds)..."
        go test -fuzz=FuzzMessageData -fuzztime=30s ./test/security/... 2>&1 || true

        echo ""
        echo "Running FuzzStreamData (30 seconds)..."
        go test -fuzz=FuzzStreamData -fuzztime=30s ./test/security/... 2>&1 || true
    else
        echo "Go version doesn't support fuzzing natively, skipping..."
    fi
fi

if $run_race; then
    print_header "RACE DETECTION TESTS"

    echo "Running tests with race detector (this may be slow)..."
    go test -race -short ./... 2>&1 | head -200 || true
fi

if $run_cover; then
    print_header "CODE COVERAGE"

    echo "Generating coverage report..."
    go test -coverprofile=coverage.out ./... 2>&1 || true
    go tool cover -func=coverage.out | tail -20 || true

    if command -v go &> /dev/null; then
        go tool cover -html=coverage.out -o coverage.html 2>&1 || true
        echo "Coverage report generated: coverage.html"
    fi
fi

print_header "TEST SUMMARY"

echo -e "${GREEN}Test execution completed!${NC}"
echo ""
echo "Test categories executed:"
$run_unit       && echo "  - Unit Tests"
$run_bench      && echo "  - Benchmark Tests"
$run_e2e        && echo "  - E2E Integration Tests"
$run_security   && echo "  - Security Tests"
$run_fuzz       && echo "  - Fuzzing Tests (if supported)"
$run_race       && echo "  - Race Detection Tests"
$run_cover      && echo "  - Code Coverage"
echo ""
echo "Check the output above for any failures or issues."
echo ""
