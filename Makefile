PROJECT_NAME := "kelon"
PKG := "github.com/Foundato/$(PROJECT_NAME)"
PKG_LIST := $(shell go list ${PKG}/... | grep -v /vendor/)
GO_FILES := $(shell find . -name '*.go' | grep -v /vendor/ | grep -v _test.go)
 
.PHONY: all dep lint vet test test-coverage build clean e2e-test load-test load-test-update-postman
 
all: build

dep: ## Get the dependencies
	@go mod download

lint: ## Lint Golang files
	@golangci-lint -c .golangci.yml run

vet: ## Run go vet
	@go vet ${PKG_LIST}

test: ## Run unittests
	@go test -short ${PKG_LIST}

test-coverage: ## Run tests with coverage
	@go test -short -coverprofile cover.out -covermode=atomic ${PKG_LIST} 
	# @cat cover.out >> coverage.txt

build: dep ## Build the binary file
	@go build -i -o out/kelon $(PKG)/cmd/kelon
 
clean: ## Remove previous build
	@rm -f $(PROJECT_NAME)/build
 
help: ## Display this help screen
	@grep -h -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

e2e-test:
	docker-compose up --build -d

	while [[ "$$(curl -s -o /dev/null -w ''%{http_code}'' localhost:8181/health)" != "200" ]]; do sleep 2; done

	docker run -v $(PWD)/test/e2e:/etc/newman -t \
		--network="kelon_compose_network" --rm\
		postman/newman run Kelon_E2E.postman_collection.json \
		--environment='kelon.postman_environment.json' \
		--reporters cli,junit \
		-n 5 \
		--reporter-junit-export kelon-results.xml || (docker-compose down --volumes; exit 1;)

	docker-compose down --volumes
	exit 0

load-test:
	docker-compose up --build -d

	while [[ "$$(curl -s -o /dev/null -w ''%{http_code}'' localhost:8181/health)" != "200" ]]; do sleep 2; done

	docker run -it -v $(PWD)/test/e2e:/output/ --rm --network="kelon_compose_network" loadimpact/k6 run /output/k6_load_tests.js || (docker-compose down --volumes; exit 1;)

	docker-compose down --volumes

	exit 0

load-test-update-postman:
	docker run -it -v $(PWD)/test/e2e:/output/ --rm loadimpact/postman-to-k6 /output/Kelon_Load.postman_collection.json -o /output/default_function_autogenerated.js