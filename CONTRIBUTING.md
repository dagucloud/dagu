# Contributing to Dagu

Thank you for considering to help improve Dagu! We welcome contributions from anyone on the internet.

You do not need to understand the whole repository before making a useful first change.

## First 15 Minutes

1. Fork the repository and clone it locally.
2. Pick a small issue, then comment on it to claim the work. Use the [Discord server](https://discord.gg/gpahPUjGRk) if you have questions or get stuck for 20 minutes; that is expected.
3. Choose the track closest to your change:

| Track | Start with | First check |
| --- | --- | --- |
| Docs / examples | A Markdown file or `examples/` | No build is required; validate a changed DAG with `dagu validate` if you have the binary available |
| UI | `ui/` | Start the backend with `make run-server`, then run `cd ui && pnpm install && pnpm dev` |
| One executor | `internal/runtime/builtin/EXECUTOR_PACKAGE` | `make test TEST_TARGET=./internal/runtime/builtin/EXECUTOR_PACKAGE` (replace `EXECUTOR_PACKAGE` with the package name) |
| API / CLI | `internal/service/frontend/api/v1` or `internal/cmd` | `make test TEST_TARGET=./internal/service/frontend/api/v1` or the matching package |

For a first PR, change only the files needed for the issue. You do not need to learn every package or run the full test suite before opening a focused PR.

## How to Contribute

We welcome contributions of all kinds, including:

- Help other users by answering questions and providing support
- Suggest new features or improvements
- Improve documentation and examples, or provide use cases
- Refactor code for better readability and maintainability
- Fix bugs or add missing tests
- Add new features based on issue discussions
- Review and provide feedback on PRs

## Development

Prerequisites depend on your track:

- [Go (latest stable)](https://go.dev/doc/install) for Go and backend changes.
- [Node.js (latest stable)](https://nodejs.org/en/download/) and [pnpm](https://pnpm.io/installation) for UI changes.
- Go is also needed for the UI development loop, since starting the backend builds it.
- Docs-only changes do not require the Go toolchain.

Building frontend assets:

```bash
make ui
```

Building binary:

```bash
make bin
```

## Running Tests

Run the smallest relevant check first. For a first code change, pass the Go
package you touched through `TEST_TARGET`:

```bash
make test TEST_TARGET=./path/to/changed/package
```

For UI changes:

```bash
cd ui
pnpm install
pnpm test
```

Docs-only and example-only changes do not need Go tests. If an example changes a DAG, validate it with the `dagu` binary when available.

The full repository check is primarily for CI and maintainers. Run it locally
when practical:

```bash
make lint
make test
```

To run tests with code coverage analysis:

```bash
make test-coverage
```

After changing Go files, run `make fmt` and check the diff before opening the PR.



## Frontend

Starting the backend server on port 8080:

```bash
make run-server
```

Starting the development server:

```bash
cd ui
pnpm install
pnpm dev
```

Navigate to [http://localhost:8081](http://localhost:8081) to view hot-reloading frontend.

### Code Standards

- Write unit tests for any new functionality
- Aim for good test coverage on new code
- Test error conditions and edge cases

### Pull Requests

Before submitting:

- [ ] The smallest relevant test or validation command passes
- [ ] `make lint` passes for Go changes when practical
- [ ] New code includes tests when behavior changes
- [ ] Documentation updated if applicable
- [ ] Commit messages following the [Go Commit Message Guidelines](https://go.dev/wiki/CommitMessage)

### Review Process

- All PRs are reviewed by [maintainers](https://github.com/dagucloud/dagu/graphs/contributors).
- Community members are encouraged to review and provide feedback.

## Issues

### Bug Reports

When reporting bugs, please include:

- Operating system and version
- Steps to reproduce the issue (example DAG yaml is very helpful)
- Expected behavior
- Actual behavior
- Relevant logs or error messages

### Feature Requests

When requesting features, please describe:

- Clearly describe the feature and its use case
- Explain why it would be valuable
- Consider backward compatibility
- Provide examples if possible

## License

By contributing to Dagu, you agree that your contributions will be licensed under the **GNU General Public License v3.0**.
