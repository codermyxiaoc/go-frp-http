# go-frp-http

A lightweight and efficient Go-based FRP (Fast Reverse Proxy) HTTP/HTTPS proxy tool for exposing local services to the internet.

## Features

- **HTTP/HTTPS Support**: Support for both HTTP and HTTPS protocols with custom SSL certificates
- **Reverse Proxy**: Expose local services through a public server using reverse proxy
- **Multiple Connections**: Configure multiple domain mappings in a single configuration file
- **Rate Limiting**: Optional bandwidth throttling for connection management
- **TLS/SSL**: Full TLS support with custom certificate configuration per domain
- **Connection Pooling**: Configurable connection pool with idle timeout management
- **Auto-Reconnection**: Automatic client reconnection with exponential backoff
- **Keep-Alive**: Built-in heartbeat mechanism to maintain connections

## Architecture

```
┌─────────────┐         ┌─────────────┐         ┌─────────────┐
│   Client    │────────▶│   Server    │◀────────│  End User   │
│  (Local)    │         │  (Public)   │         │  (Browser)  │
└─────────────┘         └─────────────┘         └─────────────┘
     │                         │
     │                         │
     ▼                         ▼
┌─────────────┐         ┌─────────────┐
│Local Service│         │HTTP/HTTPS   │
│(e.g. :3000) │         │Proxy Server │
└─────────────┘         └─────────────┘
```

## Project Structure

```
go-frp-http/
├── client/          # Client-side code
│   └── client.go
├── server/          # Server-side code
│   └── server.go
├── common/          # Shared utilities
│   ├── connect.go
│   ├── const.go
│   ├── dataTransform.go
│   ├── isError.go
│   ├── limit.go
│   ├── log.go
│   └── monitored.go
├── config.yaml      # Configuration file
├── go.mod
└── go.sum
```

## Installation

### Prerequisites

- Go 1.24.2 or higher
- Network access between client and server

### Clone the Repository

```bash
git clone https://github.com/codermyxiaoc/go-frp-http.git
cd go-frp-http
```

### Install Dependencies

```bash
go mod tidy
```

### Build

```bash
# Build server
go build -o server.exe ./server

# Build client
go build -o client.exe ./client
```

## Configuration

Edit `config.yaml` to configure both server and client settings:

### Server Configuration

```yaml
# Server settings
max-idle-conns: 2000
idle-conn-timeout: 90
conn-chan-count: 2000

# Common settings
keep-alive-time: 10
http-port: 80
main-port: 12345
secret: your-secret-key

# Rate limiting (optional)
enable-limit: false
limit-buffer-size: 4096  # in KB

# TLS/HTTPS settings
enable-tls: false
https-port: 443
certificates:
  example.com:
    cert-path: /path/to/cert.crt
    key-path: /path/to/key.key
```

### Client Configuration

```yaml
# Client settings
server-ip: your-server-ip
connections:
  - dns: your-domain.com
    local-port: 3000
    task-port: 50001
    type: http
    secret: your-secret-key
  - dns: another-domain.com
    local-port: 8080
    task-port: 50002
    type: http
    secret: your-secret-key
```

### Configuration Parameters

| Parameter | Description | Default |
|-----------|-------------|---------|
| `http-port` | HTTP server listening port | 80 |
| `https-port` | HTTPS server listening port | 443 |
| `main-port` | Main control connection port | 12345 |
| `secret` | Authentication secret key | secret |
| `server-ip` | Server IP address (client) | 127.0.0.1 |
| `max-idle-conns` | Maximum idle connections | 100 |
| `idle-conn-timeout` | Idle connection timeout (seconds) | 90 |
| `conn-chan-count` | Connection channel buffer size | 200 |
| `keep-alive-time` | Keep-alive interval (seconds) | 10 |
| `enable-limit` | Enable rate limiting | false |
| `limit-buffer-size` | Rate limit buffer size (KB) | 1024 |
| `enable-tls` | Enable HTTPS support | false |

## Usage

### 1. Start the Server

On your public server:

```bash
./server
```

The server will:
- Start HTTP server on configured port (default: 80)
- Start HTTPS server on configured port (if TLS enabled)
- Listen for client connections on main port (default: 12345)

### 2. Start the Client

On your local machine:

```bash
./client
```

The client will:
- Connect to the server using the configured server IP and main port
- Establish connections for each configured domain mapping
- Forward requests from the public server to local services

### 3. Access Your Service

Access your local service through the public domain:

```
http://your-domain.com
```

or with HTTPS (if TLS enabled):

```
https://your-domain.com
```

## Example Use Case

### Scenario

You have a local web application running on `localhost:3000` and want to expose it through `myapp.example.com`.

### Setup

1. **Server** (Public IP: 1.2.3.4):
   ```yaml
   http-port: 80
   main-port: 12345
   secret: mysecretkey123
   ```

2. **Client** (Local machine):
   ```yaml
   server-ip: 1.2.3.4
   connections:
     - dns: myapp.example.com
       local-port: 3000
       task-port: 50001
       type: http
       secret: mysecretkey123
   ```

3. **DNS**: Point `myapp.example.com` to `1.2.3.4`

4. **Run**:
   - Start server: `./server`
   - Start client: `./client`

5. **Access**: Visit `http://myapp.example.com` to access your local service!

## Security Considerations

- **Secret Key**: Always use a strong, unique secret key
- **Firewall**: Configure firewall rules to restrict access to necessary ports
- **TLS/SSL**: Use HTTPS with valid certificates for production environments
- **Rate Limiting**: Enable rate limiting to prevent abuse
- **Token Security**: Never commit sensitive tokens or keys to version control

## Dependencies

- [logrus](https://github.com/sirupsen/logrus) - Structured logging
- [viper](https://github.com/spf13/viper) - Configuration management
- [lumberjack](https://github.com/natefinch/lumberjack) - Log rotation
- [golang.org/x/time/rate](https://pkg.go.dev/golang.org/x/time/rate) - Rate limiting

## License

This project is open source and available for use.

## Contributing

Contributions are welcome! Please feel free to submit issues or pull requests.

## Author

[codermyxiaoc](https://github.com/codermyxiaoc)
