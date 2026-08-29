# Contributing to MoniGo

Thank you for your interest in contributing to MoniGo. This document explains the process and standards for contributions.

## Getting Started

```bash
git clone https://github.com/iyashjayesh/monigo.git
cd monigo
go test ./... -race
```

## Development Workflow

1. Fork the repository and create a feature branch from `main`
2. Write your code and tests
3. Ensure all checks pass: `go test ./... -race -cover && go vet ./...`
4. Submit a pull request with a clear description

## Code Standards

- Follow standard Go conventions (`gofmt`, `go vet`)
- All exported symbols must have GoDoc comments
- Tests are required for new functionality
- Use table-driven tests where appropriate
- Run with `-race` flag before submitting

## Pull Request Process

1. PRs must pass CI (tests, vet, race detector)
2. One approval from a maintainer is required
3. Commit messages should be descriptive (not "fix bug" - explain what and why)
4. Breaking changes must be documented in the PR description

## Testing

```bash
# Run all tests with race detector
go test ./... -race -count=1

# Run benchmarks
go test ./... -bench=. -benchmem

# Run with coverage
go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out
```

## Releasing

Maintainers only.

MoniGo is on the **v1 line**. `go.mod` declares
`module github.com/iyashjayesh/monigo` with no major-version suffix, and Go
requires the major version to appear in the module path from v2 onwards. A `v2.x`
tag on this module path is therefore invalid and the proxy will refuse it -- which
is what happened to `v2.0.0`, leaving `go get` serving `v1.2.0` for months while
several releases' worth of work sat unreachable.

So: **tag `v1.x.y`, and verify the proxy actually served it.** The second half is
the step whose absence caused the problem.

**Step 1 is a pull request, not a command.** It is written out below rather than
as a comment in a shell block, because a comment in a shell block is what gets
skipped -- paste the block and the only thing that runs is the tag.

### 1. Stamp the changelog (pull request)

In `CHANGELOG.md`, move everything under `## [Unreleased]` beneath a new dated
heading, and leave `## [Unreleased]` in place, empty, above it:

```markdown
## [Unreleased]

## [1.8.0] - 2026-08-30

### Fixed
- ...
```

Open it as a pull request and merge it. This is the step where someone reads
what actually shipped and says it clearly -- worth a review, and deliberately
not automated.

It also has to land **before** the tag. Stamping afterwards leaves the tag
pointing at a tree whose changelog is missing its own entry, which is what
`go get` then serves.

### 2. Tag and push

```bash
git tag -a v1.8.0 -m "v1.8.0"
git push origin v1.8.0
```

CI takes it from here. The `Release` workflow checks that the tag matches the
newest stamped section in `CHANGELOG.md` and **fails the run if it does not**,
then publishes the GitHub release using that section as the notes. If you skip
step 1, this is where you find out -- rather than shipping an empty release.

### 3. Verify the proxy served it -- not optional

```bash
curl -s https://proxy.golang.org/github.com/iyashjayesh/monigo/@latest
# must report the version just tagged

cd "$(mktemp -d)" && go mod init tmp >/dev/null
go get github.com/iyashjayesh/monigo@latest && go list -m github.com/iyashjayesh/monigo
```

This is the half CI cannot do for you: the module proxy is outside the
repository. If this does not show the new version, the tag is not usable and the
release has not actually happened, however green the CI run was -- which is
exactly how `v2.0.0` went unnoticed.

Moving to a v2 line later means renaming the module to
`github.com/iyashjayesh/monigo/v2` in `go.mod` and updating every import path in
the examples and README. That is a breaking change for every consumer, so it needs
a reason beyond the version number looking tidy.

## Reporting Issues

- Use GitHub Issues for bug reports and feature requests
- Include Go version, OS, and a minimal reproduction case
- For security vulnerabilities, see [SECURITY.md](SECURITY.md)

## License

By contributing, you agree that your contributions will be licensed under the Apache 2.0 License.
