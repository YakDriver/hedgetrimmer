.PHONY: build test clean install fmt lint vet tidy deps ci help modern modern-check copyright

default: build

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

build: ## Build binary
	@go build -o hedgetrimmer .

install: ## Install to GOPATH/bin
	@go install .

test: ## Run tests
	@go test ./...

vet: ## Run go vet
	@go vet ./...

fmt: ## Format code
	@go fmt ./...

tidy: ## Tidy go.mod
	@go mod tidy

deps: ## Download dependencies
	@go mod download

lint: ## Run golangci-lint
	@golangci-lint run

modern-check: ## Check for modern Go code
	@go fix -diff ./...

modern: ## Fix modern Go code issues
	@go fix ./...

copyright: ## Check copyright headers
	@go run github.com/YakDriver/copyplop@latest check

ci: tidy build test vet modern-check copyright ## Run all CI checks locally

clean: ## Clean build artifacts
	@rm -f hedgetrimmer coverage.out coverage.html
