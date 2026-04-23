ARG BUILDPLATFORM
FROM --platform=$BUILDPLATFORM golang:1.24.4 AS build

ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG TARGETVARIANT

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY app ./app
COPY nezha ./nezha

RUN set -eux; \
    if [ "${TARGETARCH}" = "arm" ] && [ -n "${TARGETVARIANT}" ]; then \
      export GOARM="${TARGETVARIANT#v}"; \
    fi; \
    CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/vagent ./app

FROM scratch

COPY --from=build /out/vagent /vagent
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

VOLUME ["/config"]

ENV VAGENT_CONFIG=/config/config.yml

ENTRYPOINT ["/vagent"]
