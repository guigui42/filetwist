# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e
# check=skip=InvalidBaseImagePlatform

ARG GO_IMAGE=golang:1.27.0-trixie@sha256:df98008ecd2b0ecc9f0a94d1b07e3564a9c92b555369b33d9b5f60d0765b2db7
# Bootstrap: the immutable v0.0.2 index. Future pins come from media-v* releases,
# never from the preceding application release.
ARG MEDIA_RUNTIME_IMAGE=ghcr.io/guigui42/filetwist@sha256:1313a23528b3c66d5e5167365f1b0aa7be4f70c78a1e8f1a935e178dc75a17fa

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS go-build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN test "${TARGETOS}" = linux \
    && test "${TARGETARCH}" = amd64 \
    && CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
      go build -trimpath -buildvcs=false \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/filetwist ./cmd/filetwist \
    && CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
      go build -trimpath -buildvcs=false \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/filetwist-server ./cmd/filetwist-server \
    && cp /usr/local/go/LICENSE /out/go.LICENSE

FROM ${MEDIA_RUNTIME_IMAGE}
USER 0:0
# Preserve the bootstrap's original native recipe before replacing the app recipe.
# Versioned media seeds already carry Dockerfile.runtime.
RUN if [ ! -f /usr/local/share/licenses/filetwist/Dockerfile.runtime ]; then \
      cp /usr/local/share/licenses/filetwist/Dockerfile \
        /usr/local/share/licenses/filetwist/Dockerfile.runtime; \
    fi \
    && sed -i 's/bundled Dockerfile for/bundled Dockerfile.runtime for/' \
      /opt/vips/share/licenses/libvips/SOURCE
COPY --from=go-build /out/filetwist /usr/local/bin/filetwist
COPY --from=go-build /out/filetwist-server /usr/local/bin/filetwist-server
COPY --from=go-build /out/go.LICENSE /usr/local/share/licenses/filetwist/go.LICENSE
COPY --chmod=0644 LICENSE /usr/local/share/licenses/filetwist/LICENSE
COPY --chmod=0644 internal/web/static/htmx.LICENSE /usr/local/share/licenses/filetwist/htmx.LICENSE
COPY --chmod=0644 THIRD_PARTY_NOTICES.md /usr/local/share/licenses/filetwist/THIRD_PARTY_NOTICES.md
COPY --chmod=0644 deploy/Dockerfile /usr/local/share/licenses/filetwist/Dockerfile
COPY --chmod=0755 scripts/probe-media.sh /usr/local/bin/probe-media
COPY --chmod=0755 scripts/filetwist-container.sh /usr/local/bin/filetwist-container

ARG MEDIA_RUNTIME_IMAGE
ARG VERSION=dev
ARG REVISION=unknown
LABEL org.opencontainers.image.title="Filetwist" \
      org.opencontainers.image.description="Household media converter" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.source="https://github.com/guigui42/filetwist" \
      io.github.filetwist.media-runtime="${MEDIA_RUNTIME_IMAGE}"
USER 10001:10001
WORKDIR /data
VOLUME ["/data"]
EXPOSE 8080
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
  CMD ["filetwist-container", "healthcheck"]
ENTRYPOINT ["filetwist-container"]
CMD ["serve"]
