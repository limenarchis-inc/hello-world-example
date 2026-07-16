# Hello World Example

A small Go 1.25 HTTP app for exercising path params, intermittent errors,
external calls, and configurable downstream dependency chains.

## Run Locally

```sh
make run
```

The app listens on `:8080` by default. Override with `PORT`.

## Run With Docker Compose

```sh
make docker-up
```

If port `8080` is already in use:

```sh
HOST_PORT=18080 make docker-up
```

## Test

```sh
make docker-test
```

## Configuration

`CHAIN_URLS` is a comma-separated list of downstream URLs used by `/chain`.

Set `APP_MODE=loadgen` to run the same container as a small load generator
instead of starting the API. In load-generator mode, `LOADGEN_BASE_URL` points to
the service to call and defaults to `http://localhost:8080`.

Example:

```sh
CHAIN_URLS="http://service-a:8080/hello,http://service-b:8080/hello/7/world" make run
```

Run the API and load generator together with Docker Compose:

```sh
make docker-up-loadgen
```

## Endpoints

- `GET /hello`
- `GET /hello/{int}/world`
- `GET /hello/with/uuid/{uuid}/world`
- `GET /hello/{int}/multi/{int}/path`
- `GET /error`
- `GET /external/call`
- `GET /chain`
- `GET /healthz`
