# Build the inference-agent binary
FROM registry.access.redhat.com/ubi8/go-toolset:1.22.9-3.1742991062 as builder

# Copy in the go src
WORKDIR /go/src/github.com/kserve/kserve
COPY go.mod  go.mod
COPY go.sum  go.sum
RUN go mod download

COPY pkg/    pkg/
COPY cmd/    cmd/

# Build
USER root
RUN CGO_ENABLED=0 GOOS=linux GOFLAGS=-mod=mod go build -a -o agent ./cmd/agent

# Copy the inference-agent into a thin image
FROM registry.access.redhat.com/ubi8/ubi-minimal:latest

RUN mkdir -p /home/kserve && \
    touch /etc/passwd /etc/group /etc/shadow && \
    echo 'kserve:x:1000:1000::/home/kserve:/bin/bash' >> /etc/passwd && \
    echo 'kserve:*:18573:0:99999:7:::' >> /etc/shadow && \
    echo 'kserve:x:1000:' >> /etc/group && \
    chown -R 1000:1000 /home/kserve

COPY third_party/ third_party/
WORKDIR /ko-app
COPY --from=builder /go/src/github.com/kserve/kserve/agent /ko-app/
USER 1000:1000

ENTRYPOINT ["/ko-app/agent"]
