# Contributing to Director

Thank you for your interest in contributing to Director! This document provides guidelines and instructions for contributing.

## Development Setup

### Prerequisites

- Go 1.26+
- Docker and Docker Compose (for e2e tests)
- [Task](https://taskfile.dev/) (task runner)

### Getting Started

```bash
git clone https://github.com/swchck/director.git
cd director
```

### Running Tests

```bash
# Unit tests (no infrastructure needed)
task test

# E2E tests (requires running services)
task up          # start Postgres, Redis, Directus
task test:e2e    # run e2e tests
task down        # stop services

# Full e2e cycle (start, test, stop)
task e2e

# Tests with coverage
task test:cover
task test:cover:html   # open in browser
```

Always run tests with the `-race` flag (the Taskfile does this by default).

### Linting

```bash
task lint    # runs go vet + go build + go test
```

The project uses [golangci-lint](https://golangci-lint.run/) with the configuration in `.golangci.yml`.

## Code Style

- **Go 1.26**, standard library preferred over external dependencies
- **Logging**: use the `log.Logger` interface — never import a specific logger in library packages
- **Errors**: sentinel errors for expected cases, `fmt.Errorf` wrapping for context
- **Functional options** pattern for configuration (`WithLogger`, `WithCache`, etc.)
- **Generic types**: `Collection[T]`, `Singleton[T]`, `View[T]`, etc.
- **Test files**: use `_test` package suffix (black-box testing)
- **E2e tests**: use `//go:build e2e` tag

## Pull Request Process

1. Fork the repository and create a feature branch from `main`
2. Write tests for new functionality
3. Ensure all tests pass: `task test` and `task e2e`
4. Ensure linting passes: `task lint`
5. Keep commits focused — one logical change per commit
6. Write clear commit messages explaining _why_, not just _what_
7. Open a pull request against `main`

## Architecture

Key principles:

- **Source-agnostic**: the `source/` package interfaces decouple sync from any CMS
- **Lock-free reads**: all config/view reads use `atomic.Pointer`
- **Sync writes**: `Swap()` and view recomputation are synchronous within the OnChange chain
- **No panics**: recover user-callback panics, return errors

## Package Responsibilities

| Package     | Owns                                                |
| ----------- | --------------------------------------------------- |
| `source/`   | Data source interfaces                              |
| `directus/` | Directus HTTP client, schema, ACL, flows, WebSocket |
| `config/`   | In-memory stores, views, translations               |
| `manager/`  | Sync orchestration, leader/follower, debounce       |
| `storage/`  | Snapshot interfaces and Postgres implementation     |
| `notify/`   | Cross-replica event interfaces and implementations  |
| `registry/` | Instance heartbeat interfaces and implementations   |
| `cache/`    | Caching interfaces and implementations              |
| `log/`      | Logger interface and slog adapter                   |

## Reporting Bugs

Open a [GitHub issue](https://github.com/swchck/director/issues) with:

- Go version and OS
- Minimal reproduction steps
- Expected vs actual behavior
- Relevant logs or error messages

## Questions?

Open a discussion on GitHub or reach out via issues.
