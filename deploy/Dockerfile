# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e
# check=skip=InvalidBaseImagePlatform

ARG GO_IMAGE=golang:1.27.0-trixie@sha256:df98008ecd2b0ecc9f0a94d1b07e3564a9c92b555369b33d9b5f60d0765b2db7
ARG DEBIAN_IMAGE=debian:trixie-slim@sha256:abc9cb88a5587630d7f915f47b23b0668fe250fbfc6457aa4d52b534c1bbf73f

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

FROM ${DEBIAN_IMAGE} AS vips-build

ARG DEBIAN_SNAPSHOT=20260905T000000Z
ARG VIPS_VERSION=8.18.6
ARG VIPS_SHA256=3c41e1d5458081bfa4a5bc54e116c46259c75c6760a18027764555632b9dda3e

RUN printf '%s\n' \
      'Types: deb' \
      "URIs: http://snapshot.debian.org/archive/debian/${DEBIAN_SNAPSHOT}" \
      'Suites: trixie trixie-updates' \
      'Components: main non-free' \
      'Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg' \
      'Check-Valid-Until: no' \
      '' \
      'Types: deb' \
      "URIs: http://snapshot.debian.org/archive/debian-security/${DEBIAN_SNAPSHOT}" \
      'Suites: trixie-security' \
      'Components: main non-free' \
      'Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg' \
      'Check-Valid-Until: no' \
      > /etc/apt/sources.list.d/debian.sources \
    && apt-get update \
    && apt-get install --yes --no-install-recommends \
      build-essential \
      ca-certificates \
      curl \
      libexpat1-dev \
      libglib2.0-dev \
      libheif-dev \
      libjpeg62-turbo-dev \
      liblcms2-dev \
      libpng-dev \
      libtiff-dev \
      libwebp-dev \
      meson \
      ninja-build \
      pkg-config \
    && curl --fail --location --show-error \
      "https://github.com/libvips/libvips/releases/download/v${VIPS_VERSION}/vips-${VIPS_VERSION}.tar.xz" \
      --output /tmp/vips.tar.xz \
    && echo "${VIPS_SHA256}  /tmp/vips.tar.xz" | sha256sum --check --strict \
    && mkdir /tmp/vips \
    && tar --extract --xz --file /tmp/vips.tar.xz \
      --directory /tmp/vips --strip-components=1 \
    && meson setup /tmp/vips/build /tmp/vips \
      --prefix=/opt/vips \
      --libdir=lib \
      --buildtype=release \
      --strip \
      -Darchive=disabled \
      -Dcfitsio=disabled \
      -Dcgif=disabled \
      -Dcplusplus=false \
      -Ddeprecated=false \
      -Dexamples=false \
      -Dexif=disabled \
      -Dfftw=disabled \
      -Dfontconfig=disabled \
      -Dheif=enabled \
      -Dheif-module=disabled \
      -Dhighway=disabled \
      -Dimagequant=disabled \
      -Dintrospection=disabled \
      -Djpeg=enabled \
      -Djpeg-xl=disabled \
      -Dlcms=enabled \
      -Dmagick=disabled \
      -Dmatio=disabled \
      -Dmodules=disabled \
      -Dnifti=disabled \
      -Dopenexr=disabled \
      -Dopenjpeg=disabled \
      -Dopenslide=disabled \
      -Dorc=disabled \
      -Dpangocairo=disabled \
      -Dpdfium=disabled \
      -Dpng=enabled \
      -Dpoppler=disabled \
      -Dquantizr=disabled \
      -Draw=disabled \
      -Drsvg=disabled \
      -Dspng=disabled \
      -Dtiff=enabled \
      -Duhdr=disabled \
      -Dwebp=enabled \
      -Dzlib=enabled \
    && meson compile -C /tmp/vips/build \
    && meson install -C /tmp/vips/build

RUN install --directory /opt/vips/share/licenses/libvips \
    && install --mode=0644 /tmp/vips/LICENSE /opt/vips/share/licenses/libvips/LICENSE \
    && printf '%s\n' \
      "https://github.com/libvips/libvips/releases/download/v${VIPS_VERSION}/vips-${VIPS_VERSION}.tar.xz" \
      "SHA256: ${VIPS_SHA256}" \
      'Built without source modifications; see the bundled Dockerfile for build options.' \
      > /opt/vips/share/licenses/libvips/SOURCE

FROM ${DEBIAN_IMAGE}

ARG DEBIAN_SNAPSHOT=20260905T000000Z
ARG VERSION=dev
ARG REVISION=unknown

LABEL org.opencontainers.image.title="Filetwist" \
      org.opencontainers.image.description="Household media converter" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.source="https://github.com/guigui42/filetwist"

RUN printf '%s\n' \
      'Types: deb' \
      "URIs: http://snapshot.debian.org/archive/debian/${DEBIAN_SNAPSHOT}" \
      'Suites: trixie trixie-updates' \
      'Components: main non-free' \
      'Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg' \
      'Check-Valid-Until: no' \
      '' \
      'Types: deb' \
      "URIs: http://snapshot.debian.org/archive/debian-security/${DEBIAN_SNAPSHOT}" \
      'Suites: trixie-security' \
      'Components: main non-free' \
      'Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg' \
      'Check-Valid-Until: no' \
      > /etc/apt/sources.list.d/debian.sources \
    && apt-get update \
    && apt-get install --yes --no-install-recommends \
      ca-certificates \
      ffmpeg \
      intel-media-va-driver-non-free \
      libexpat1 \
      libglib2.0-0t64 \
      libheif-plugin-aomenc \
      libheif-plugin-dav1d \
      libheif-plugin-libde265 \
      libheif-plugin-x265 \
      libheif1 \
      libjpeg62-turbo \
      liblcms2-2 \
      libpng16-16t64 \
      libtiff6 \
      libva2 \
      libwebp7 \
      libwebpdemux2 \
      libwebpmux3 \
      zlib1g \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 10001 --create-home \
      --home-dir /home/filetwist --shell /usr/sbin/nologin filetwist \
    && install --directory --owner=10001 --group=10001 --mode=0750 /data

COPY --from=vips-build /opt/vips /opt/vips
COPY --from=go-build /out/filetwist /usr/local/bin/filetwist
COPY --from=go-build /out/filetwist-server /usr/local/bin/filetwist-server
RUN install --directory --mode=0755 /usr/local/share/licenses/filetwist
COPY --chmod=0644 LICENSE /usr/local/share/licenses/filetwist/LICENSE
COPY --chmod=0644 internal/web/static/htmx.LICENSE /usr/local/share/licenses/filetwist/htmx.LICENSE
COPY --from=go-build /out/go.LICENSE /usr/local/share/licenses/filetwist/go.LICENSE
COPY --chmod=0644 THIRD_PARTY_NOTICES.md /usr/local/share/licenses/filetwist/THIRD_PARTY_NOTICES.md
COPY --chmod=0644 deploy/Dockerfile /usr/local/share/licenses/filetwist/Dockerfile
RUN printf 'Binary package\tBinary version\tSource package\tSource version\n' \
      > /usr/local/share/licenses/filetwist/debian-packages.tsv \
    && dpkg-query --show --showformat='${binary:Package}\t${Version}\t${source:Package}\t${source:Version}\n' \
      > /tmp/filetwist-debian-packages.tsv \
    && LC_ALL=C sort /tmp/filetwist-debian-packages.tsv \
      >> /usr/local/share/licenses/filetwist/debian-packages.tsv \
    && rm /tmp/filetwist-debian-packages.tsv \
    && printf '%s\n' "${DEBIAN_SNAPSHOT}" \
      > /usr/local/share/licenses/filetwist/debian-snapshot.txt \
    && ffmpeg -hide_banner -buildconf \
      > /usr/local/share/licenses/filetwist/ffmpeg-buildconf.txt 2>&1
COPY --chmod=0755 scripts/probe-media.sh /usr/local/bin/probe-media
COPY --chmod=0755 scripts/filetwist-container.sh /usr/local/bin/filetwist-container

ENV PATH="/opt/vips/bin:${PATH}" \
    LD_LIBRARY_PATH="/opt/vips/lib" \
    DATA_DIR="/data"

RUN printf '%s\n' '/opt/vips/lib' > /etc/ld.so.conf.d/libvips.conf \
    && ldconfig \
    && probe-media capabilities \
    && filetwist --version \
    && filetwist-server version

USER 10001:10001
WORKDIR /data
VOLUME ["/data"]
EXPOSE 8080
STOPSIGNAL SIGTERM

# The healthcheck derives its URL from the same LISTEN_ADDRESS the server
# binds, so a non-default port needs no argument here. HEALTHCHECK_URL
# overrides it when the server is reached another way.
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
  CMD ["filetwist-container", "healthcheck"]

ENTRYPOINT ["filetwist-container"]
CMD ["serve"]
