
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
curl -sSL https://raw.githubusercontent.com/noelukwa/localbase/main/install.sh | sudo sh
```

## usage

✨ _ensure caddy is setup and running_

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

start the localbase service in foreground:

```sh
localbase start
```

start the localbase service in detached mode:

```sh
localbase start -d
```

stop the localbase service:

```sh
localbase stop
```
