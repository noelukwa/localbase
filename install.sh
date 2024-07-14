#!/bin/sh
set -e

# Determine architecture
ARCH=$(uname -m)
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

# Determine OS
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case $OS in
    linux|darwin)
        ;;
    *)
        echo "Unsupported operating system: $OS"
        exit 1
        ;;
esac

# Fetch the latest version tag from GitHub API
LATEST_VERSION=$(curl -s https://api.github.com/repos/noelukwa/localbase/releases/latest | grep 'tag_name' | cut -d\" -f4)

if [ -z "$LATEST_VERSION" ]; then
    echo "Failed to fetch the latest version. Please check your internet connection and try again."
    exit 1
fi

# Construct download URL
DOWNLOAD_URL="https://github.com/noelukwa/localbase/releases/download/${LATEST_VERSION}/localbase_${OS}_${ARCH}.tar.gz"

# Output the download URL for debugging
echo "Download URL: $DOWNLOAD_URL"

# Download and install
echo "Downloading Localbase ${LATEST_VERSION} for ${OS} ${ARCH}..."
if ! curl -L -o localbase.tar.gz "$DOWNLOAD_URL"; then
    echo "Download failed. Please check the URL and try again."
    exit 1
fi

# Check if the downloaded file is empty or contains an error message
if [ ! -s localbase.tar.gz ] || [ "$(head -c 9 localbase.tar.gz)" = "Not Found" ]; then
    echo "Error: Downloaded file is empty or contains 'Not Found'. The release might not exist for your OS/architecture combination."
    rm localbase.tar.gz
    exit 1
fi

# Extract and install
if ! tar -xzf localbase.tar.gz; then
    echo "Error: Failed to extract the archive. The downloaded file might be corrupted."
    rm localbase.tar.gz
    exit 1
fi

if [ ! -f localbase ]; then
    echo "Error: The 'localbase' binary was not found in the extracted archive."
    rm localbase.tar.gz
    exit 1
fi

if ! sudo mv localbase /usr/local/bin/; then
    echo "Error: Failed to move the 'localbase' binary to /usr/local/bin/. Do you have sudo permissions?"
    rm localbase localbase.tar.gz
    exit 1
fi

rm localbase.tar.gz

echo "Localbase has been successfully installed to /usr/local/bin/localbase"
echo "You can now run 'localbase' from anywhere in your terminal."