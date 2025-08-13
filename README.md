# LocalBase

A secure, lightweight tool for provisioning .local domains with automatic HTTPS support. LocalBase simplifies local development by managing Caddy reverse proxy configurations and mDNS service discovery.

## Features

- 🔒 **Secure by default** - Token-based authentication and TLS encryption
- 🚀 **Zero-config HTTPS** - Automatic certificate generation and management  
- 🌐 **mDNS integration** - Automatic `.local` domain resolution
- 🔄 **Hot reloading** - Dynamic domain addition/removal without restarts
- 🎯 **Production ready** - Comprehensive logging, error handling, and monitoring
- ⚡ **Lightweight** - Minimal resource usage with connection pooling

## Requirements

- [Caddy](https://caddyserver.com/) - Web server with automatic HTTPS
- [Go](https://golang.org/) 1.21+ - For installation from source

## Installation

### 🚀 Quick Install (Recommended)

```bash
curl -sSL https://raw.githubusercontent.com/noelukwa/localbase/main/install.sh | sudo sh
```

### 🍺 Homebrew

```bash
brew tap noelukwa/tap
brew install localbase
```

### 💾 Binary Download

```bash
# Download latest release for your platform
wget https://github.com/noelukwa/localbase/releases/latest/download/localbase_linux_x86_64.tar.gz
tar -xzf localbase_linux_x86_64.tar.gz
sudo mv localbase /usr/local/bin/
```

### 🛠️ Go Install

```bash
go install github.com/noelukwa/localbase@latest
```

### 🔧 Build from Source

```bash
git clone https://github.com/noelukwa/localbase.git
cd localbase
go build -o localbase .
```

## Quick Start

1. **Start LocalBase service**:

   ```bash
   localbase start
   ```

2. **Add a domain** (in another terminal):

   ```bash
   localbase add myapp --port 3000
   ```

3. **Start your application** on port 3000

4. **Visit** [https://myapp.local](https://myapp.local) 🎉

## Usage

### Service Management

```bash
# Start in foreground
localbase start

# Start in daemon mode  
localbase start -d

# Stop service
localbase stop

# Check service status
localbase status
```

### Domain Management

```bash
# Add domain pointing to local service
localbase add hello --port 3000

# Remove domain
localbase remove hello

# List all domains
localbase list

# Health check
localbase ping
```

### Configuration

LocalBase stores configuration in:

- **macOS**: `~/Library/Application Support/localbase/`
- **Linux**: `~/.config/localbase/`
- **Windows**: `%APPDATA%\localbase\`

Default configuration:

```json
{
  "caddy_admin": "http://localhost:2019",
  "admin_address": "localhost:2025"
}
```

## Development

### Running Tests

```bash
go test ./... -v
```

### Running Benchmarks

```bash
go test -bench=. -benchmem
```

### Code Coverage

```bash
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Run tests (`go test ./...`)
4. Commit changes (`git commit -m 'Add amazing feature'`)
5. Push to branch (`git push origin feature/amazing-feature`)  
6. Open a Pull Request

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## Troubleshooting

### Common Issues

**"Caddy not found"**

```bash
# Install Caddy
brew install caddy  # macOS
sudo apt install caddy  # Ubuntu/Debian  
```

**"Permission denied"**

```bash
# Check file permissions
ls -la ~/.config/localbase/
```

**"Connection refused"**

```bash
# Check if service is running
localbase status
```

### Debug Mode

Enable debug logging:

```bash
LOCALBASE_LOG_LEVEL=debug localbase start
```
