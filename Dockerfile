# Build the manager binary
FROM registry.access.redhat.com/ubi8/go-toolset:1.22.9-3.1742991062 as builder

# Copy in the go src
WORKDIR /go/src/github.com/kserve/kserve
COPY go.mod  go.mod
COPY go.sum  go.sum

RUN  go mod download

COPY cmd/    cmd/
COPY pkg/    pkg/

# Build
USER root
RUN CGO_ENABLED=0 GOOS=linux GOFLAGS=-mod=mod go build -a -o manager ./cmd/manager

# Use distroless as minimal base image to package the manager binary
FROM registry.access.redhat.com/ubi8/ubi-minimal:latest

RUN mkdir -p /home/kserve && \
    touch /etc/passwd /etc/group /etc/shadow && \
    echo 'kserve:x:1000:1000::/home/kserve:/bin/bash' >> /etc/passwd && \
    echo 'kserve:*:18573:0:99999:7:::' >> /etc/shadow && \
    echo 'kserve:x:1000:' >> /etc/group && \
    chown -R 1000:1000 /home/kserve

COPY third_party/ /third_party/
COPY --from=builder /go/src/github.com/kserve/kserve/manager /
USER 1000:1000

ENTRYPOINT ["/manager"]
