#!/bin/bash

# Automatic install script for the Middle-Monitor agent
# Usage: MIDDLE_MONITOR_INSTALL_TOKEN=xxx curl -fsSL https://your-api-url/api/v1/agents/download/install | bash
#    or: curl -fsSL https://your-api-url/api/v1/agents/download/install?token=xxx | bash

set -e

# --download-only fetches the binary and its checksum and stops there. It is the
# supported path for configuration-management tools, which have no business
# piping a remote script into a root shell.
DOWNLOAD_ONLY=0
for arg in "$@"; do
    case "$arg" in
        --download-only) DOWNLOAD_ONLY=1 ;;
    esac
done

# Token required (like Datadog/Terraform Cloud). Source: env MIDDLE_MONITOR_INSTALL_TOKEN or ?token= in the URL
# The backend injects INSTALL_TOKEN_INJECTED when ?token= is present in the URL
INSTALL_TOKEN_INJECTED=""
INSTALL_TOKEN="${INSTALL_TOKEN_INJECTED:-$MIDDLE_MONITOR_INSTALL_TOKEN}"
if [ -z "$INSTALL_TOKEN" ] && [ "$DOWNLOAD_ONLY" -eq 0 ]; then
    echo "Install token required."
    echo ""
    echo "   Option 1 (env, recommended for CI/CD):"
    echo "   export MIDDLE_MONITOR_INSTALL_TOKEN=your_token"
    echo "   curl -fsSL <API_URL>/api/v1/agents/download/install | bash"
    echo ""
    echo "   Option 2 (inline) - the variable must come BEFORE bash, not curl:"
    echo "   curl -fsSL <API_URL>/api/v1/agents/download/install | MIDDLE_MONITOR_INSTALL_TOKEN=your_token bash"
    echo ""
    echo "   Option 3 (token in the URL):"
    echo "   curl -fsSL <API_URL>/api/v1/agents/download/install?token=your_token | bash"
    echo ""
    echo "   Generate a token in Middle Monitor: Settings -> API keys -> Agent install tokens"
    exit 1
fi

# Make sure we have a TTY for sudo (avoids blocking when run via pipe)
if [ ! -t 0 ] && [ -e /dev/tty ]; then
    echo "Warning: running via pipe: sudo may block. Alternative:"
    echo "   curl -fsSL <API_URL>/api/v1/agents/download/install -o install.sh && bash install.sh"
    echo ""
fi

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

# Root shells on minimal images often have no sudo.
if [ "$(id -u)" -eq 0 ] && ! command -v sudo &> /dev/null; then
    sudo() { "$@"; }
fi

AGENT_NAME="middle-monitor-agent-${OS}-${ARCH}"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/middle-monitor"

if [ "$DOWNLOAD_ONLY" -eq 1 ]; then
    API_BASE="${API_URL%/}"
    OUT="${MIDDLE_MONITOR_DOWNLOAD_PATH:-./${AGENT_NAME}}"
    echo "Downloading ${AGENT_NAME} to ${OUT}"
    curl -fSL --connect-timeout 10 --max-time 300 "${API_BASE}/api/v1/agents/download/${OS}/${ARCH}" -o "${OUT}" </dev/null
    chmod +x "${OUT}"

    # Verify against the published digest rather than trusting the transfer.
    EXPECTED=$(curl -fsSL "${API_BASE}/api/v1/agents/download/${OS}/${ARCH}/sha256" | cut -d' ' -f1)
    if [ -n "${EXPECTED}" ]; then
        if command -v sha256sum &> /dev/null; then
            ACTUAL=$(sha256sum "${OUT}" | cut -d' ' -f1)
        else
            ACTUAL=$(shasum -a 256 "${OUT}" | cut -d' ' -f1)
        fi
        if [ "${EXPECTED}" != "${ACTUAL}" ]; then
            echo "Checksum mismatch: expected ${EXPECTED}, got ${ACTUAL}"
            rm -f "${OUT}"
            exit 1
        fi
        echo "sha256 verified: ${ACTUAL}"
    fi
    echo "Version: $("${OUT}" --version 2>/dev/null || echo unknown)"
    exit 0
fi

echo "Installing Middle-Monitor Agent..."
echo "   OS: ${OS}"
echo "   Architecture: ${ARCH}"
echo "   API: ${API_URL}"

# Ask for the host name (optional) to link to an existing host in the UI
# Always ask when a TTY is available (even if a config already exists)
DEFAULT_HOST=$(hostname)
if [ -n "${MIDDLE_MONITOR_HOST_NAME:-}" ]; then
    HOST_NAME="$MIDDLE_MONITOR_HOST_NAME"
elif (: </dev/tty) 2>/dev/null; then
    echo ""
    echo "Host name to link services to an existing host in Middle Monitor"
    read -p "   Host name (Enter = ${DEFAULT_HOST}): " HOST_NAME_INPUT </dev/tty
    HOST_NAME="${HOST_NAME_INPUT:-$DEFAULT_HOST}"
else
    HOST_NAME="$DEFAULT_HOST"
fi

# Create the configuration directory
sudo mkdir -p ${CONFIG_DIR}

# Download the agent
echo "Downloading the agent..."
API_BASE="${API_URL%/}"
DOWNLOAD_URL="${API_BASE}/api/v1/agents/download/${OS}/${ARCH}"
echo "   URL: ${DOWNLOAD_URL}"

TEMP_FILE=$(mktemp)
if command -v curl &> /dev/null; then
    # Close stdin for curl (avoids blocking when the script is run via pipe)
    if ! curl -fSL --connect-timeout 10 --max-time 120 --progress-bar -H "Connection: close" "${DOWNLOAD_URL}" -o "${TEMP_FILE}" </dev/null; then
        echo "Agent download failed"
        echo "   Manual test: curl -I ${DOWNLOAD_URL}"
        echo "   Check that the backend is running at ${API_URL}"
        rm -f "${TEMP_FILE}"
        exit 1
    fi
elif command -v wget &> /dev/null; then
    if ! wget -q --timeout=120 "${DOWNLOAD_URL}" -O "${TEMP_FILE}"; then
        echo "Agent download failed"
        echo "   Check that the backend is running at ${API_URL}"
        rm -f "${TEMP_FILE}"
        exit 1
    fi
else
    echo "curl or wget is required to download the agent"
    exit 1
fi

# Check that the downloaded file is not empty
echo "Download complete"
if [ ! -s "${TEMP_FILE}" ]; then
    echo "The downloaded file is empty"
    rm -f "${TEMP_FILE}"
    exit 1
fi

# Check that it is an executable binary (ELF or Mach-O magic number)
echo "Verifying the binary..."
MAGIC=$(od -An -tx1 -N4 "${TEMP_FILE}" | tr -d ' \n')
if [ "${MAGIC}" != "7f454c46" ] && [ "${MAGIC}" != "cffaedfe" ]; then
    echo "The downloaded file is not a valid binary"
    echo "   File contents (first 100 characters):"
    head -c 100 "${TEMP_FILE}" | cat -A
    rm -f "${TEMP_FILE}"
    exit 1
fi

# Move the file to its final location
echo "Installing into ${INSTALL_DIR}..."
sudo mv "${TEMP_FILE}" "${INSTALL_DIR}/${AGENT_NAME}"

# Make it executable
sudo chmod +x "${INSTALL_DIR}/${AGENT_NAME}"

# Create the symlink
echo "Creating the symlink..."
if [ -L "${INSTALL_DIR}/middle-monitor-agent" ]; then
    sudo rm "${INSTALL_DIR}/middle-monitor-agent"
elif [ -f "${INSTALL_DIR}/middle-monitor-agent" ]; then
    sudo rm "${INSTALL_DIR}/middle-monitor-agent"
fi

# Create a symlink
sudo ln -s "${INSTALL_DIR}/${AGENT_NAME}" "${INSTALL_DIR}/middle-monitor-agent"

# Check that the symlink works
if [ ! -e "${INSTALL_DIR}/middle-monitor-agent" ]; then
    echo "Failed to create the symlink"
    exit 1
fi

# Create the configuration file if it does not exist
if [ ! -f "${CONFIG_DIR}/config.yaml" ]; then
    echo "Creating the configuration file..."
    sudo tee "${CONFIG_DIR}/config.yaml" > /dev/null <<EOF
api:
  url: "${API_URL}"
  api_key: "${INSTALL_TOKEN}"

host:
  name: "${HOST_NAME}"
  service: "${HOST_NAME}"

metrics:
  cpu: true
  ram: true
  disk: true
  network: true

interval: 60
EOF
    echo "Configuration created at ${CONFIG_DIR}/config.yaml"
else
    # Update the host name AND api_key in the existing config
    # (api_key is required to create services in the UI)
    if [ "$OS" = "darwin" ]; then
        sudo sed -i '' "s|^  url: .*|  url: \"${API_URL}\"|" "${CONFIG_DIR}/config.yaml"
        sudo sed -i '' "s|^  name: .*|  name: \"${HOST_NAME}\"|" "${CONFIG_DIR}/config.yaml"
        sudo sed -i '' "s|^  service: .*|  service: \"${HOST_NAME}\"|" "${CONFIG_DIR}/config.yaml"
        sudo sed -i '' "s|^  api_key: .*|  api_key: \"${INSTALL_TOKEN}\"|" "${CONFIG_DIR}/config.yaml"
        # If api_key does not exist yet, add it after url (no $'\n' for sh compatibility)
        if ! grep -q "api_key:" "${CONFIG_DIR}/config.yaml"; then
            sudo sed -i '' 's|^  url: .*|  url: "'"${API_URL}"'"\
  api_key: "'"${INSTALL_TOKEN}"'"|' "${CONFIG_DIR}/config.yaml"
        fi
    else
        sudo sed -i "s|^  url: .*|  url: \"${API_URL}\"|" "${CONFIG_DIR}/config.yaml"
        sudo sed -i "s|^  name: .*|  name: \"${HOST_NAME}\"|" "${CONFIG_DIR}/config.yaml"
        sudo sed -i "s|^  service: .*|  service: \"${HOST_NAME}\"|" "${CONFIG_DIR}/config.yaml"
        sudo sed -i "s|^  api_key: .*|  api_key: \"${INSTALL_TOKEN}\"|" "${CONFIG_DIR}/config.yaml"
        # If api_key does not exist yet, add it after url (no $'\n' for sh compatibility)
        if ! grep -q "api_key:" "${CONFIG_DIR}/config.yaml"; then
            sudo sed -i 's|^  url: .*|  url: "'"${API_URL}"'"\
  api_key: "'"${INSTALL_TOKEN}"'"|' "${CONFIG_DIR}/config.yaml"
        fi
    fi
    echo "Configuration updated (host: ${HOST_NAME}, api_key set)"
fi

# Install as a service
if [ "$OS" = "linux" ]; then
    echo "Installing the systemd service..."
    sudo tee /etc/systemd/system/middle-monitor-agent.service > /dev/null <<EOF
[Unit]
Description=Middle-Monitor Agent
After=network.target

[Service]
Type=simple
User=root
ExecStart=${INSTALL_DIR}/middle-monitor-agent
Restart=always
RestartSec=10
Environment="MIDDLE_MONITOR_CONFIG=${CONFIG_DIR}/config.yaml"

[Install]
WantedBy=multi-user.target
EOF
    sudo systemctl daemon-reload
    sudo systemctl enable middle-monitor-agent
    sudo systemctl start middle-monitor-agent
    echo "Service installed and started"

elif [ "$OS" = "darwin" ]; then
    echo "Installing the launchd service..."
    LOG_DIR="${HOME}/Library/Logs/middle-monitor"
    mkdir -p "${LOG_DIR}"
    sudo tee ~/Library/LaunchAgents/com.middlemonitor.agent.plist > /dev/null <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.middlemonitor.agent</string>
    <key>ProgramArguments</key>
    <array>
        <string>${INSTALL_DIR}/middle-monitor-agent</string>
    </array>
    <key>EnvironmentVariables</key>
    <dict>
        <key>MIDDLE_MONITOR_CONFIG</key>
        <string>${CONFIG_DIR}/config.yaml</string>
    </dict>
    <key>StandardOutPath</key>
    <string>${LOG_DIR}/agent.out.log</string>
    <key>StandardErrorPath</key>
    <string>${LOG_DIR}/agent.err.log</string>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
</dict>
</plist>
EOF
    launchctl unload ~/Library/LaunchAgents/com.middlemonitor.agent.plist 2>/dev/null || true
    launchctl load ~/Library/LaunchAgents/com.middlemonitor.agent.plist
    sleep 1
    if [ -f "${LOG_DIR}/agent.err.log" ]; then
        echo "Warning: service errors (see ${LOG_DIR}/agent.err.log):"
        tail -5 "${LOG_DIR}/agent.err.log" 2>/dev/null || true
    fi
    echo "Service installed and started"
    echo "Logs available in: ${LOG_DIR}/"
fi

echo ""
echo "Installation complete!"
echo ""
echo "Information:"
echo "   - Binary: ${INSTALL_DIR}/middle-monitor-agent"
echo "   - Configuration: ${CONFIG_DIR}/config.yaml"
echo ""
echo "Check the status:"
if [ "$OS" = "linux" ]; then
    echo "   sudo systemctl status middle-monitor-agent"
else
    echo "   launchctl list | grep middlemonitor"
fi
echo ""
echo "Edit the configuration:"
echo "   sudo nano ${CONFIG_DIR}/config.yaml"
echo ""
