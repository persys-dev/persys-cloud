# Troubleshooting Dependency Issues

## Network/Proxy Errors with `go mod tidy`

If you get errors like:
```
Get "https://proxy.golang.org/...": EOF
```

This is usually a network connectivity issue with the Go proxy. Here are solutions:

### Solution 1: Retry with Direct Access

```bash
# Use direct GitHub access instead of proxy
GOPROXY=direct go mod download
go mod tidy
```

### Solution 2: Use Different Go Proxy

```bash
# Try Google's proxy
export GOPROXY=https://proxy.golang.org,direct
go mod download

# Or use goproxy.io
export GOPROXY=https://goproxy.io,direct
go mod download

# Or disable proxy entirely (slower but reliable)
export GOPROXY=direct
go mod download
```

### Solution 3: Build Without Tidy

You don't need to run `go mod tidy` to build:

```bash
# Just download and build
go mod download
go build -o bin/persys-agent ./cmd/agent
```

### Solution 4: Use Vendor Directory

```bash
# Download dependencies once
go mod download
go mod vendor

# Build using vendor (no network needed)
go build -mod=vendor -o bin/persys-agent ./cmd/agent
```

## Specific Docker Client Issues

If you see errors related to `github.com/distribution/reference`:

```bash
# Clear module cache
go clean -modcache

# Download with direct access
GOPROXY=direct go get github.com/distribution/reference@v0.5.0

# Try again
go mod download
go build -o bin/persys-agent ./cmd/agent
```

## Alternative: Use Pre-Built Binary (Future)

If dependency issues persist, you can:

1. **Use Docker image** (coming soon):
   ```bash
   docker pull persys/compute-agent:latest
   ```

2. **Download pre-built binary** (coming soon):
   ```bash
   curl -L https://github.com/persys/compute-agent/releases/latest/download/persys-agent-linux-amd64 -o persys-agent
   chmod +x persys-agent
   ```

## Quick Build Script

Save this as `build.sh`:

```bash
#!/bin/bash
set -e

echo "Clearing cache..."
go clean -modcache

echo "Downloading dependencies (direct mode)..."
GOPROXY=direct go mod download

echo "Building..."
go build -o bin/persys-agent ./cmd/agent

echo "✓ Build complete: bin/persys-agent"
```

Then:
```bash
chmod +x build.sh
./build.sh
```

## Environment Variables Reference

```bash
# Direct access (no proxy)
export GOPROXY=direct

# Use proxy with fallback to direct
export GOPROXY=https://proxy.golang.org,direct

# Disable checksum database (if needed)
export GOSUMDB=off

# Private modules (if you fork the repo)
export GOPRIVATE=github.com/yourorg/*
```

## Verify Setup

```bash
# Check Go version (needs 1.21+)
go version

# Check environment
go env GOPROXY
go env GOPATH

# Test download
go mod download -x  # -x shows what it's doing
```

## Still Having Issues?

1. **Check your internet connection**
   ```bash
   curl -I https://proxy.golang.org
   ```

2. **Check for firewall/proxy blocking Go modules**
   ```bash
   # Test direct GitHub access
   curl -I https://github.com
   ```

3. **Try on a different network** (corporate networks often block Go proxy)

4. **Use a VPN** if in a restricted region

5. **Build in a Docker container** (controlled environment):
   ```bash
   docker run --rm -v "$PWD":/workspace -w /workspace golang:1.21 \
     sh -c "go mod download && go build -o bin/persys-agent ./cmd/agent"
   ```

## Common Error Messages & Fixes

| Error | Cause | Fix |
|-------|-------|-----|
| `EOF` | Network timeout | Use `GOPROXY=direct` |
| `410 Gone` | Module removed/moved | Update go.mod versions |
| `403 Forbidden` | Rate limiting | Wait or use different proxy |
| `x509: certificate` | SSL issues | Update CA certificates |
| `checksum mismatch` | Corrupted cache | `go clean -modcache` |

## Success Check

After successful build:
```bash
./bin/persys-agent --help
# Should show usage info (or run the agent)

ls -lh bin/persys-agent
# Should show ~20-30MB binary
```

## Report Issues

If none of these work, please report:
1. Go version: `go version`
2. OS: `uname -a`
3. Full error message
4. Network environment (corporate/home/VPN)

GitHub Issues: https://github.com/persys/compute-agent/issues
