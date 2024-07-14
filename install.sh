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
LATEST_VERSION=$(curl -s https://api.github.com/repos/noelukwa/localblade/releases/latest | grep 'tag_name' | cut -d\" -f4)

# Construct download URL
DOWNLOAD_URL="https://github.com/noelukwa/localblade/releases/download/${LATEST_VERSION}/localbase_${OS}_${ARCH}.tar.gz"

# Download and install
echo "Downloading Localbase ${LATEST_VERSION} for ${OS} ${ARCH}..."
curl -L -o localbase.tar.gz $DOWNLOAD_URL
tar -xzf localbase.tar.gz
sudo mv localbase /usr/local/bin/
rm localbase.tar.gz

echo "Localbase has been installed to /usr/local/bin/localbase"
echo "You can now run 'localbase' from anywhere in your terminal."
