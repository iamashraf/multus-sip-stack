FROM golang:1.26 AS builder
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /dispatcher-operator ./cmd

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /dispatcher-operator /dispatcher-operator
USER 65532:65532
ENTRYPOINT ["/dispatcher-operator"]
