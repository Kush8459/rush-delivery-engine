# One image recipe for all six Go binaries. Select via --build-arg SERVICE=…
#   docker build --build-arg SERVICE=order-service -t rush/order-service .
# docker-compose uses the same mechanism per service.

# ---- build ----
FROM golang:1.25-alpine AS build
ARG SERVICE

WORKDIR /src

# Go-modules caching: copy mod files first so an unchanged go.sum doesn't
# bust the download layer.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Static binary, no libc dependency → safe for distroless/static.
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o /out/app ./cmd/${SERVICE}

# ---- runtime ----
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/app /app
COPY config/config.docker.yaml /config/config.yaml

ENV CONFIG_PATH=/config/config.yaml
USER nonroot:nonroot

ENTRYPOINT ["/app"]
