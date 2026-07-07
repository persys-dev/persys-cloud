#!/bin/bash
# Build script for Persys Compute Agent
# Handles network issues automatically

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo "================================================"
echo "  Persys Compute Agent - Build Script"
echo "================================================"
echo ""

# Check Go version
echo "Checking Go version..."
GO_VERSION=$(go version | awk '{print $3}' | sed 's/go//')
REQUIRED_VERSION="1.21"

if [ "$(printf '%s\n' "$REQUIRED_VERSION" "$GO_VERSION" | sort -V | head -n1)" != "$REQUIRED_VERSION" ]; then 
    echo -e "${RED}✗ Go $REQUIRED_VERSION or higher required (found $GO_VERSION)${NC}"
    exit 1
fi
echo -e "${GREEN}✓ Go $GO_VERSION${NC}"
echo ""

# Create bin directory
mkdir -p bin

# Try different methods to download dependencies
echo "Downloading dependencies..."

# Method 1: Try with default proxy
echo "  Attempt 1: Using default Go proxy..."
if go mod download 2>/dev/null; then
    echo -e "${GREEN}  ✓ Download successful${NC}"
else
    echo -e "${YELLOW}  ✗ Default proxy failed, trying direct access...${NC}"
    
    # Method 2: Try with direct access
    echo "  Attempt 2: Using direct GitHub access..."
    if GOPROXY=direct go mod download 2>/dev/null; then
        echo -e "${GREEN}  ✓ Download successful (direct mode)${NC}"
    else
        echo -e "${YELLOW}  ✗ Direct access failed, trying alternative proxy...${NC}"
        
        # Method 3: Try alternative proxy
        echo "  Attempt 3: Using goproxy.io..."
        if GOPROXY=https://goproxy.io,direct go mod download 2>/dev/null; then
            echo -e "${GREEN}  ✓ Download successful (goproxy.io)${NC}"
        else
            echo -e "${RED}  ✗ All download methods failed${NC}"
            echo ""
            echo "Troubleshooting tips:"
            echo "1. Check your internet connection"
            echo "2. Try: export GOPROXY=direct"
            echo "3. See docs/TROUBLESHOOTING_DEPENDENCIES.md"
            exit 1
        fi
    fi
fi

echo ""

# Build
echo "Building Persys Compute Agent..."
if go build -o bin/persys-agent ./cmd/agent; then
    echo -e "${GREEN}✓ Build successful!${NC}"
    echo ""
    
    # Show binary info
    echo "Binary information:"
    ls -lh bin/persys-agent
    echo ""
    
    # Success message
    echo "================================================"
    echo -e "${GREEN}  Build Complete!${NC}"
    echo "================================================"
    echo ""
    echo "To run the agent:"
    echo "  PERSYS_TLS_ENABLED=false ./bin/persys-agent"
    echo ""
    echo "For production deployment, see:"
    echo "  docs/DEPLOYMENT.md"
    echo ""
else
    echo -e "${RED}✗ Build failed${NC}"
    exit 1
fi
