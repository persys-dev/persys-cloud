# Contributing to Persys Compute Agent

Thank you for your interest in contributing to the Persys Compute Agent! This document provides guidelines and instructions for contributing.

## Code of Conduct

We are committed to providing a welcoming and inclusive environment. Please be respectful and professional in all interactions.

## Getting Started

### Prerequisites

- Go 1.21 or later
- Docker Engine
- docker-compose (optional)
- KVM/libvirt (optional)
- protoc compiler
- Basic understanding of gRPC and distributed systems

### Development Setup

1. **Fork and Clone**

```bash
git clone https://github.com/YOUR_USERNAME/compute-agent.git
cd compute-agent
```

2. **Install Development Tools**

```bash
make dev-setup
```

This installs:
- protoc-gen-go
- protoc-gen-go-grpc
- golangci-lint

3. **Install Dependencies**

```bash
make deps
```

4. **Generate Protobuf Code**

```bash
make proto
```

5. **Build and Run**

```bash
make build
make dev-certs
./bin/persys-agent
```

## Development Workflow

### 1. Create a Branch

```bash
git checkout -b feature/your-feature-name
# or
git checkout -b fix/bug-description
```

Branch naming conventions:
- `feature/` - New features
- `fix/` - Bug fixes
- `docs/` - Documentation changes
- `refactor/` - Code refactoring
- `test/` - Test additions/changes

### 2. Make Changes

Write clean, well-documented code following Go best practices.

### 3. Write Tests

All new code should include tests:

```bash
# Run tests
make test

# Run with coverage
go test -v -race -coverprofile=coverage.txt ./...

# View coverage
go tool cover -html=coverage.txt
```

### 4. Format and Lint

```bash
# Format code
make fmt

# Run linters
make lint
```

### 5. Commit

Write clear, descriptive commit messages:

```bash
git commit -m "feat: add support for Firecracker runtime"
git commit -m "fix: resolve race condition in reconciliation loop"
git commit -m "docs: update deployment guide"
```

Commit message format:
- `feat:` - New feature
- `fix:` - Bug fix
- `docs:` - Documentation
- `test:` - Tests
- `refactor:` - Code refactoring
- `perf:` - Performance improvement
- `chore:` - Maintenance tasks

### 6. Push and Create PR

```bash
git push origin feature/your-feature-name
```

Then create a Pull Request on GitHub.

## Code Style Guidelines

### Go Code Style

Follow standard Go conventions:

1. **Use gofmt**: All code must be formatted with `gofmt`
2. **Variable naming**: Use camelCase, avoid abbreviations
3. **Error handling**: Always check errors, never ignore them
4. **Comments**: Document exported functions and types
5. **Package organization**: Keep packages focused and cohesive

Example:

```go
// ProcessWorkload applies a workload specification to the runtime.
// It returns the workload status and any error encountered.
func ProcessWorkload(ctx context.Context, spec *WorkloadSpec) (*WorkloadStatus, error) {
	if spec == nil {
		return nil, fmt.Errorf("spec cannot be nil")
	}
	
	// Implementation
	...
	
	return status, nil
}
```

### Error Handling

```go
// Good - wrap errors with context
if err := runtime.Create(ctx, workload); err != nil {
	return fmt.Errorf("failed to create workload %s: %w", workload.ID, err)
}

// Bad - lose error context
if err := runtime.Create(ctx, workload); err != nil {
	return err
}
```

### Logging

Use structured logging with appropriate levels:

```go
logger.Debugf("Processing workload: %s", id)
logger.Infof("Workload %s created successfully", id)
logger.Warnf("Failed to delete old workload: %v", err)
logger.Errorf("Critical error in runtime: %v", err)
```

## Testing Guidelines

### Unit Tests

Write unit tests for all business logic:

```go
func TestApplyWorkload_Success(t *testing.T) {
	// Setup
	mockStore := new(MockStore)
	mockRuntime := new(MockRuntime)
	// ... setup expectations
	
	// Execute
	result, err := manager.ApplyWorkload(ctx, workload)
	
	// Assert
	assert.NoError(t, err)
	assert.NotNil(t, result)
	
	// Verify
	mockStore.AssertExpectations(t)
	mockRuntime.AssertExpectations(t)
}
```

### Integration Tests

Tag integration tests:

```go
// +build integration

func TestDockerRuntime_E2E(t *testing.T) {
	// Requires actual Docker daemon
	...
}
```

Run integration tests:

```bash
go test -tags=integration ./...
```

### Test Coverage

Aim for >80% coverage on new code:

```bash
go test -coverprofile=coverage.txt ./...
go tool cover -func=coverage.txt
```

## Adding New Features

### Adding a New Runtime

1. **Implement Runtime Interface**

```go
// internal/runtime/myruntime.go
type MyRuntime struct {
	// fields
}

func (r *MyRuntime) Create(ctx context.Context, workload *models.Workload) error {
	// implementation
}

// ... implement all interface methods
```

2. **Add Configuration**

```go
// internal/config/config.go
type Config struct {
	// ...
	MyRuntimeEnabled bool
	MyRuntimeURI     string
}
```

3. **Register in Main**

```go
// cmd/agent/main.go
if cfg.MyRuntimeEnabled {
	myRuntime, err := runtime.NewMyRuntime(cfg.MyRuntimeURI, logger)
	if err == nil {
		runtimeMgr.Register(myRuntime)
	}
}
```

4. **Add Tests**

5. **Update Documentation**

### Adding API Methods

1. **Update Proto**

```protobuf
// api/proto/agent.proto
service AgentService {
	// ... existing methods
	rpc MyNewMethod(MyRequest) returns (MyResponse);
}
```

2. **Regenerate Code**

```bash
make proto
```

3. **Implement Handler**

```go
// internal/grpc/server.go
func (s *Server) MyNewMethod(ctx context.Context, req *pb.MyRequest) (*pb.MyResponse, error) {
	// implementation
}
```

4. **Add Tests**

5. **Update Documentation**

## Documentation

### Code Documentation

Document all exported types and functions:

```go
// WorkloadManager coordinates workload lifecycle operations.
// It ensures idempotency through revision tracking and maintains
// consistency between desired and actual state.
type WorkloadManager struct {
	// ...
}

// ApplyWorkload creates or updates a workload based on the provided
// specification. It implements idempotency through revision checking:
// if the revision ID matches an existing workload, the operation is skipped.
//
// Parameters:
//   - ctx: Context for cancellation and deadlines
//   - workload: Workload specification to apply
//
// Returns:
//   - status: Current workload status after operation
//   - skipped: true if operation was skipped due to matching revision
//   - error: any error encountered during operation
func (m *WorkloadManager) ApplyWorkload(ctx context.Context, workload *models.Workload) (*models.WorkloadStatus, bool, error) {
	// ...
}
```

### User Documentation

Update relevant documentation:
- README.md - Overview and quick start
- docs/ARCHITECTURE.md - Architecture details
- docs/DEPLOYMENT.md - Deployment examples
- API documentation in proto files

## Pull Request Process

### Before Submitting

- [ ] Code is formatted (`make fmt`)
- [ ] All tests pass (`make test`)
- [ ] Linters pass (`make lint`)
- [ ] New code has tests
- [ ] Documentation is updated
- [ ] Commit messages are clear

### PR Description Template

```markdown
## Description
Brief description of changes

## Type of Change
- [ ] Bug fix
- [ ] New feature
- [ ] Breaking change
- [ ] Documentation update

## Testing
How was this tested?

## Checklist
- [ ] Tests added/updated
- [ ] Documentation updated
- [ ] No breaking changes (or documented)
```

### Review Process

1. Maintainer reviews code
2. CI/CD runs automated tests
3. Address any feedback
4. Once approved, maintainer merges

## Reporting Issues

### Bug Reports

Include:
- Persys Agent version
- Operating system
- Runtime environment (Docker/libvirt versions)
- Steps to reproduce
- Expected vs actual behavior
- Logs (if applicable)

### Feature Requests

Include:
- Use case description
- Proposed solution
- Alternatives considered
- Additional context

## Release Process

Maintainers follow this process for releases:

1. Update version in code
2. Update CHANGELOG.md
3. Create git tag
4. Build and push Docker image
5. Create GitHub release
6. Update documentation

## Questions?

- GitHub Discussions: Ask questions
- GitHub Issues: Report bugs
- Email: dev@persys.cloud

## License

By contributing, you agree that your contributions will be licensed under the same license as the project.
