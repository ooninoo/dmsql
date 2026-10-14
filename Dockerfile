FROM golang:1.26.3-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
# The DM driver is a local module referenced by go.mod's replace directive.
COPY pkg/dmsdk ./pkg/dmsdk
RUN go mod download

COPY cmd ./cmd
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/dmsql ./cmd

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /out/dmsql /usr/local/bin/dmsql
USER 65532:65532
WORKDIR /work
ENTRYPOINT ["/usr/local/bin/dmsql"]
