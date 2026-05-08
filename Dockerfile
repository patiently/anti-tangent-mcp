# syntax=docker/dockerfile:1.7

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/anti-tangent-mcp ./cmd/anti-tangent-mcp

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/anti-tangent-mcp /anti-tangent-mcp
ENTRYPOINT ["/anti-tangent-mcp"]
