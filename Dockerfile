FROM golang:1.25 AS build

WORKDIR /src

COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/hello-world-app .

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app
COPY --from=build /out/hello-world-app /app/hello-world-app

ENV PORT=8080
EXPOSE 8080

ENTRYPOINT ["/app/hello-world-app"]
