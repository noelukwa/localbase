
# localbase

localbase is a lightweight tool for provisioning secure .local domains. It simplifies the process of setting up local development environments with HTTPS support.

## requirements

- [caddy](https://caddyserver.com/)
- [go](https://golang.org/)

## installation

```
go install github.com/noelukwa/localbase@latest
```

## usage

add a new domain:
```
localbase add example.local --port 3000
```

remove a domain:
```
localbase remove example.local
```

list all configured domains:
```
localbase list
```

start the localbase service:
```
localbase start
```

stop the localbase service:

```
localbase stop
```






