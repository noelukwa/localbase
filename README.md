
# localbase

localbase is a lightweight tool for provisioning secure .local domains. It simplifies the process of setting up local development environments with HTTPS support.

## requirements

- [caddy](https://caddyserver.com/)
- [go](https://golang.org/)

## installation

```go
go install github.com/noelukwa/localbase@latest
```

```sh
curl -sSL https://raw.githubusercontent.com/noelukwa/localblade/main/install.sh | sudo sh
```

## usage

add a new domain:

```sh
localbase add example.local --port 3000
```

remove a domain:

```sh
localbase remove example.local
```

list all configured domains:

```sh
localbase list
```

start the localbase service:

```sh
localbase start
```

stop the localbase service:

```sh
localbase stop
```
