#!/bin/bash

# Builds the agent for every platform the API serves.

set -e

VERSION=${1:-"latest"}
BUILD_DIR="dist"
AGENT_NAME="middle-monitor-agent"

echo "Building Middle Monitor agent ${VERSION}"

mkdir -p ${BUILD_DIR}

PLATFORMS=(
    "linux/amd64"
    "linux/arm64"
    "darwin/amd64"
    "darwin/arm64"
)

for PLATFORM in "${PLATFORMS[@]}"; do
    GOOS=${PLATFORM%%/*}
    GOARCH=${PLATFORM##*/}
    
    OUTPUT_NAME="${AGENT_NAME}-${GOOS}-${GOARCH}"

    echo "Building for ${GOOS}/${GOARCH}..."
    # Use CGO_ENABLED=0 for static binaries to avoid dependency issues
    env CGO_ENABLED=0 GOOS=${GOOS} GOARCH=${GOARCH} go build -ldflags "-X main.Version=${VERSION} -s -w" -o ${BUILD_DIR}/${OUTPUT_NAME} .

    # The checksum is published next to the binary so a deployment can verify
    # what it downloaded (Ansible get_url checksum:, sha256sum -c).
    if command -v sha256sum &> /dev/null; then
        (cd ${BUILD_DIR} && sha256sum "${OUTPUT_NAME}" > "${OUTPUT_NAME}.sha256")
    else
        (cd ${BUILD_DIR} && shasum -a 256 "${OUTPUT_NAME}" > "${OUTPUT_NAME}.sha256")
    fi

    echo "Built ${OUTPUT_NAME}"
done

# The API reads this file to report the version it is serving on
# GET /api/v1/agents/latest.
echo "${VERSION}" > ${BUILD_DIR}/VERSION

echo ""
echo "Build complete. Files are in ${BUILD_DIR}/"
ls -lh ${BUILD_DIR}/

