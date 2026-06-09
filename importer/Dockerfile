# syntax=docker/dockerfile:1.7
FROM golang:1.24.1-bookworm AS builder
WORKDIR /src
COPY main.go go.mod* ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/importer .

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=builder /out/importer /app/importer
USER nonroot:nonroot
EXPOSE 3161
ENTRYPOINT ["/app/importer"]
