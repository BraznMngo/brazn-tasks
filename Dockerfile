# syntax=docker/dockerfile:1@sha256:87999aa3d42bdc6bea60565083ee17e86d1f3339802f543c0d03998580f9cb89
FROM --platform=$BUILDPLATFORM node:24.18.0-alpine@sha256:a0b9bf06e4e6193cf7a0f58816cc935ff8c2a908f81e6f1a95432d679c54fbfd AS frontendbuilder

WORKDIR /build

ENV PNPM_CACHE_FOLDER=.cache/pnpm/
ENV PUPPETEER_SKIP_DOWNLOAD=true
ENV CYPRESS_INSTALL_BINARY=0

COPY frontend/pnpm-lock.yaml frontend/package.json frontend/pnpm-workspace.yaml ./
RUN npm install -g corepack && corepack enable && \
    pnpm install --frozen-lockfile
COPY frontend/ ./
ARG RELEASE_VERSION=dev
# The public path the app is served under. frontend/vite.config.ts already
# reads this — `base: env.VIKUNJA_FRONTEND_BASE` — and loadEnv() with an empty
# prefix takes it from the environment, so declaring it here is all that was
# missing for a build to be able to set it. It has to be a BUILD input: `base`
# is baked into every emitted asset URL and, through import.meta.env.BASE_URL,
# into the Vue router's base, so a reverse proxy that strips a path prefix
# inbound cannot fix the links the client generates outbound.
#
# Default `/` is exactly Vite's own default, so a build that passes nothing is
# byte-for-byte what it was before this line existed. Percy's deploy workflow
# passes `/brazntasks/` (BraznMngo/Percy, BRA-1009).
ARG VIKUNJA_FRONTEND_BASE=/
ENV VIKUNJA_FRONTEND_BASE=$VIKUNJA_FRONTEND_BASE
RUN echo "{\"VERSION\": \"${RELEASE_VERSION/-g/-}\"}" > src/version.json && pnpm run build

FROM --platform=$BUILDPLATFORM ghcr.io/techknowlogick/xgo:go-1.26.x@sha256:b00957d8fec512c4748a5fafe17197be1d8c0bf704b271fc4aa128f5ddf40414 AS apibuilder

RUN go install github.com/magefile/mage@latest && \
    mv /go/bin/mage /usr/local/go/bin

WORKDIR /go/src/code.vikunja.io/api
COPY . ./
COPY --from=frontendbuilder /build/dist ./frontend/dist

ARG TARGETOS TARGETARCH TARGETVARIANT RELEASE_VERSION
ENV RELEASE_VERSION=$RELEASE_VERSION

RUN export PATH=$PATH:$GOPATH/bin && \
	mage build:clean && \
    (cd build && mage release:xgo vikunja "${TARGETOS}/${TARGETARCH}/${TARGETVARIANT}")

RUN mkdir -p /tmp && chmod 1777 /tmp

#  ┬─┐┬ ┐┌┐┐┌┐┐┬─┐┬─┐
#  │┬┘│ │││││││├─ │┬┘
#  ┘└┘┘─┘┘└┘┘└┘┴─┘┘└┘

# The actual image
FROM scratch

# image.source must point at the corresponding source for what is in the image
# (AGPL-3.0 section 13), which is this fork, not upstream.
LABEL org.opencontainers.image.authors='hello@braznmngo.com'
LABEL org.opencontainers.image.url='https://github.com/BraznMngo/brazn-tasks'
LABEL org.opencontainers.image.documentation='https://github.com/BraznMngo/brazn-tasks#readme'
LABEL org.opencontainers.image.source='https://github.com/BraznMngo/brazn-tasks'
LABEL org.opencontainers.image.licenses='AGPLv3'
LABEL org.opencontainers.image.title='Brazn Tasks'
LABEL org.opencontainers.image.description='Brazn Tasks, a modified fork of Vikunja v2.4.0. Not affiliated with or endorsed by the Vikunja project.'

WORKDIR /app/vikunja
ENTRYPOINT [ "/app/vikunja/vikunja" ]
EXPOSE 3456

COPY --from=apibuilder --chown=1000:1000 --chmod=1777 /tmp /tmp

USER 1000

ENV VIKUNJA_SERVICE_ROOTPATH=/app/vikunja/
ENV VIKUNJA_DATABASE_PATH=/db/vikunja.db

COPY --from=apibuilder /build/vikunja-* vikunja
COPY --from=apibuilder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
