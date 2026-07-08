# Contributing

We welcome contributions to ai-deck-converter! This document provides guidelines and instructions for contributing.

## Getting Started

1. Fork the repository
2. Clone your fork locally
3. Create a feature branch (`git checkout -b feature/my-feature`)
4. Make your changes
5. Run tests to ensure everything works
6. Commit your changes with messages formatted as [conventional commits](https://www.conventionalcommits.org/en/v1.0.0/#summary)
7. Push to your fork
8. Open a pull request

## Development

Build the CLI:
```sh
go build ./cmd/ai-deck-converter
```

Run all tests:
```sh
go test ./...
```

Run a specific test:
```sh
go test ./convert -run 'TestGolden/01_single_model'
```

Regenerate golden test expectations:
```sh
go test ./convert -run TestGolden -update
go test ./revert -run TestGolden -update
```

## Code Standards

- Use standard Go formatting (`gofmt`)
- Follow Go naming conventions and idioms
- Add tests for new functionality
- Update documentation as needed
- Keep commits focused and well-described - follow [conventional commit messages](https://www.conventionalcommits.org/en/v1.0.0/#summary)

## Testing

Before submitting a PR:

1. Run the full test suite: `go test ./...`
2. Verify round-trip conversions work: `go test ./revert -run TestRoundTrip`
3. Check any golden tests for changes: `git diff convert/testdata revert/testdata`

For golden test changes, the PR diff should clearly show what changed and why.

## Pull Request Process

1. Update the CHANGELOG.md with any new features or breaking changes
2. Ensure all tests pass locally
3. Provide a clear description of the changes in your PR
4. Link any related issues
5. Wait for review and address feedback

## Reporting Issues

If you find a bug or have a feature request, please open an issue with:

- Clear title and description
- Steps to reproduce (for bugs)
- Expected vs. actual behavior
- Go version and OS information

## Security

For security vulnerabilities, see [SECURITY.md](SECURITY.md).

## License

By contributing to this project, you agree that your contributions will be licensed under its Apache 2.0 License.
