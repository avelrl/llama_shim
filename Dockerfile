FROM golang:1.26-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /out/shim ./cmd/shim && \
    CGO_ENABLED=0 go build -o /out/shimctl ./cmd/shimctl

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
WORKDIR /app

COPY --from=build /out/shim /usr/local/bin/shim
COPY --from=build /out/shimctl /usr/local/bin/shimctl

EXPOSE 8080
CMD ["/usr/local/bin/shim", "-config", "/app/config.yaml"]
