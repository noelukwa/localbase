#!/bin/sh
set -e

# This script will be updated automatically during the release process.
# Do not modify it manually.

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

# Placeholder for the latest version
VERSION="v0.0.0"

# Construct download URL
DOWNLOAD_URL="https://github.com/noelukwa/localblade/releases/download/${VERSION}/localbase_${OS}_${ARCH}.tar.gz"

# Download and install
echo "Downloading Localbase ${VERSION} for ${OS} ${ARCH}..."
curl -L -o localbase.tar.gz $DOWNLOAD_URL
tar -xzf localbase.tar.gz
sudo mv localbase /usr/local/bin/
rm localbase.tar.gz

echo "Localbase has been installed to /usr/local/bin/localbase"
echo "You can now run 'localbase' from anywhere in your terminal."