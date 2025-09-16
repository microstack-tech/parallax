# Support setting various labels on the final image
ARG COMMIT=""
ARG VERSION=""
ARG BUILDNUM=""

# Build Prlx in a stock Go builder container
FROM golang:1.25-alpine as builder

RUN apk add --no-cache gcc musl-dev linux-headers git

# Get dependencies - will also be cached if we won't change go.mod/go.sum
COPY go.mod /parallax/
COPY go.sum /parallax/
RUN cd /parallax && go mod download

ADD . /parallax
RUN cd /parallax && go run build/ci.go install ./cmd/prlx

# Pull Prlx into a second stage deploy alpine container
FROM alpine:latest

RUN apk add --no-cache ca-certificates
COPY --from=builder /parallax/build/bin/prlx /usr/local/bin/

EXPOSE 8545 8546 30303 30303/udp
ENTRYPOINT ["prlx"]

# Add some metadata labels to help programatic image consumption
ARG COMMIT=""
ARG VERSION=""
ARG BUILDNUM=""

LABEL commit="$COMMIT" version="$VERSION" buildnum="$BUILDNUM"
