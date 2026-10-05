#!/bin/bash

# Middle-Monitor agent automatic update script
# Usage: curl -fsSL https://your-api-url/api/v1/agents/download/update | bash
# Or directly: ./update.sh

set -e

# API_URL is injected by the backend handler based on the request
# This default will be replaced by the backend when serving the script
API_URL="${MIDDLE_MONITOR_API_URL:-http://localhost:8080}"

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

# Normalize the architecture
case $ARCH in
    x86_64)
        ARCH="amd64"
        ;;
    arm64|aarch64)
        ARCH="arm64"
        ;;
    *)
        echo "Unsupported architecture: $ARCH"
        exit 1
        ;;
esac

# Determine the OS
if [ "$OS" != "linux" ] && [ "$OS" != "darwin" ]; then
    echo "Unsupported OS: $OS"
    exit 1
fi

AGENT_NAME="middle-monitor-agent-${OS}-${ARCH}"
INSTALL_DIR="/usr/local/bin"
CURRENT_AGENT="${INSTALL_DIR}/middle-monitor-agent"

echo "Updating Middle Monitor agent"
echo "   OS: ${OS}"
echo "   Architecture: ${ARCH}"
echo "   API: ${API_URL}"

# Check that the agent is installed
if [ ! -f "${CURRENT_AGENT}" ] && [ ! -L "${CURRENT_AGENT}" ]; then
    echo "The agent is not installed. Use the install script first."
    exit 1
fi

# Download the new version, and verify it before stopping anything: a failed
# download must leave the running agent untouched.
echo "Downloading the new version..."
DOWNLOAD_URL="${API_URL}/api/v1/agents/download/${OS}/${ARCH}"

TEMP_FILE=$(mktemp)
if command -v curl &> /dev/null; then
    if ! curl -fsSL "${DOWNLOAD_URL}" -o "${TEMP_FILE}"; then
        echo "Agent download failed"
        rm -f "${TEMP_FILE}"
        exit 1
    fi
elif command -v wget &> /dev/null; then
    if ! wget -q "${DOWNLOAD_URL}" -O "${TEMP_FILE}"; then
        echo "Agent download failed"
        rm -f "${TEMP_FILE}"
        exit 1
    fi
else
    echo "curl or wget is required to download the agent"
    exit 1
fi

# Check that the downloaded file is not empty
if [ ! -s "${TEMP_FILE}" ]; then
    echo "The downloaded file is empty"
    rm -f "${TEMP_FILE}"
    exit 1
fi

# Check that it is an executable binary
if ! file "${TEMP_FILE}" | grep -qE "(ELF|Mach-O)"; then
    echo "The downloaded file is not a valid binary"
    rm -f "${TEMP_FILE}"
    exit 1
fi

# Stop the service before updating
echo "Stopping the service..."
if [ "$OS" = "linux" ]; then
    sudo systemctl stop middle-monitor-agent 2>/dev/null || true
elif [ "$OS" = "darwin" ]; then
    launchctl unload ~/Library/LaunchAgents/com.middlemonitor.agent.plist 2>/dev/null || true
fi

# Back up the previous version (optional)
BACKUP_FILE="${INSTALL_DIR}/${AGENT_NAME}.backup"
if [ -f "${INSTALL_DIR}/${AGENT_NAME}" ]; then
    echo "Backing up the previous version..."
    sudo cp "${INSTALL_DIR}/${AGENT_NAME}" "${BACKUP_FILE}" 2>/dev/null || true
fi

# Replace the binary
echo "Installing the new version..."
sudo mv "${TEMP_FILE}" "${INSTALL_DIR}/${AGENT_NAME}"

# Make it executable
sudo chmod +x "${INSTALL_DIR}/${AGENT_NAME}"

# Update the symlink
if [ -L "${CURRENT_AGENT}" ]; then
    sudo rm "${CURRENT_AGENT}"
elif [ -f "${CURRENT_AGENT}" ]; then
    sudo rm "${CURRENT_AGENT}"
fi
sudo ln -s "${INSTALL_DIR}/${AGENT_NAME}" "${CURRENT_AGENT}"

# Restart the service
echo "Restarting the service..."
if [ "$OS" = "linux" ]; then
    sudo systemctl daemon-reload
    sudo systemctl start middle-monitor-agent
    sleep 1
    if sudo systemctl is-active --quiet middle-monitor-agent; then
        echo "Service restarted"
    else
        echo "The service did not start. Check the logs:"
        echo "   sudo systemctl status middle-monitor-agent"
        echo "   sudo journalctl -u middle-monitor-agent -n 20"
    fi
elif [ "$OS" = "darwin" ]; then
    launchctl load ~/Library/LaunchAgents/com.middlemonitor.agent.plist
    sleep 1
    if launchctl list | grep -q com.middlemonitor.agent; then
        echo "Service restarted"
    else
        echo "The service did not start. Check the logs:"
        echo "   tail -f ~/Library/Logs/middle-monitor/agent.err.log"
    fi
fi

echo ""
echo "Update complete"
echo ""
echo "Information:"
echo "   - New version: ${INSTALL_DIR}/middle-monitor-agent"
if [ -f "${BACKUP_FILE}" ]; then
    echo "   - Previous version backed up: ${BACKUP_FILE}"
fi

# Show the configuration location
CONFIG_PATH="${MIDDLE_MONITOR_CONFIG:-/etc/middle-monitor/config.yaml}"
echo "   - Configuration file: ${CONFIG_PATH}"
echo ""
echo "Check the status:"
if [ "$OS" = "linux" ]; then
    echo "   sudo systemctl status middle-monitor-agent"
else
    echo "   launchctl list | grep middlemonitor"
fi
echo ""
