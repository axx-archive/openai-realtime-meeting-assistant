ARG BONFIRE_GO_BUILD_IMAGE=golang:1.26-bookworm@sha256:3f6236bd765f898a2a3c2946112b04097814c4529d44534674700cd07b9c6b4c
ARG BONFIRE_RUNTIME_IMAGE=debian:bookworm-slim@sha256:63a496b5d3b99214b39f5ed70eb71a61e590a77979c79cbee4faf991f8c0783e
ARG BONFIRE_DEBIAN_SNAPSHOT=20260720T000000Z

FROM ${BONFIRE_GO_BUILD_IMAGE} AS build

ARG BONFIRE_RELEASE_COMMIT=unqualified
ARG BONFIRE_GIT_TREE_DIGEST=unqualified
ARG BONFIRE_BUILD_CONFIG_SHA256=unqualified
ARG BONFIRE_BUILD_TRANSITIVE_INPUTS_SHA256=unqualified
ARG BONFIRE_SOURCE_ARCHIVE_SHA256=unqualified
ARG BONFIRE_BUILD_INPUT_MANIFEST_SHA256=unqualified
ARG BONFIRE_DEBIAN_SNAPSHOT
ARG SOURCE_DATE_EPOCH=0

RUN rm -f /etc/apt/sources.list.d/debian.sources \
	&& printf 'deb [check-valid-until=no] http://snapshot.debian.org/archive/debian/%s bookworm main\ndeb [check-valid-until=no] http://snapshot.debian.org/archive/debian-security/%s bookworm-security main\n' "$BONFIRE_DEBIAN_SNAPSHOT" "$BONFIRE_DEBIAN_SNAPSHOT" > /etc/apt/sources.list \
	&& apt-get -o Acquire::Check-Valid-Until=false update \
	&& apt-get install -y --no-install-recommends pkg-config libopus-dev \
	&& rm -rf /var/lib/apt/lists/* \
	&& mkdir -p /out \
	&& dpkg-query -W -f='${Package}=${Version}\n' | LC_ALL=C sort > /out/release-build-packages.txt

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -ldflags "-buildid= -X main.mediaSoakBuildCommit=${BONFIRE_RELEASE_COMMIT} -X main.mediaSoakBuildTreeDigest=${BONFIRE_GIT_TREE_DIGEST} -X main.mediaSoakBuildConfigDigest=${BONFIRE_BUILD_CONFIG_SHA256} -X main.mediaSoakBuildInputsDigest=${BONFIRE_BUILD_TRANSITIVE_INPUTS_SHA256} -X main.mediaSoakBuildSourceArchiveDigest=${BONFIRE_SOURCE_ARCHIVE_SHA256} -X main.releaseEmbeddedBuildInputManifestDigest=${BONFIRE_BUILD_INPUT_MANIFEST_SHA256}" -o /out/meetingassist .

FROM ${BONFIRE_RUNTIME_IMAGE} AS meetingassist-runtime

ARG BONFIRE_RELEASE_COMMIT=unqualified
ARG BONFIRE_GIT_TREE_DIGEST=unqualified
ARG BONFIRE_BUILD_CONFIG_SHA256=unqualified
ARG BONFIRE_BUILD_TRANSITIVE_INPUTS_SHA256=unqualified
ARG BONFIRE_SOURCE_ARCHIVE_SHA256=unqualified
ARG BONFIRE_BUILD_INPUT_MANIFEST_SHA256=unqualified
ARG BONFIRE_DEBIAN_SNAPSHOT

LABEL org.opencontainers.image.revision="${BONFIRE_RELEASE_COMMIT}" \
	  xyz.thebonfire.git-tree-digest="${BONFIRE_GIT_TREE_DIGEST}" \
	  xyz.thebonfire.config-digest="${BONFIRE_BUILD_CONFIG_SHA256}" \
	  xyz.thebonfire.transitive-inputs-digest="${BONFIRE_BUILD_TRANSITIVE_INPUTS_SHA256}" \
	  xyz.thebonfire.source-archive-digest="${BONFIRE_SOURCE_ARCHIVE_SHA256}" \
	  xyz.thebonfire.build-input-manifest-digest="${BONFIRE_BUILD_INPUT_MANIFEST_SHA256}" \
	  xyz.thebonfire.attestation="unsigned-external-verification-required"

RUN rm -f /etc/apt/sources.list.d/debian.sources \
	&& printf 'deb [check-valid-until=no] http://snapshot.debian.org/archive/debian/%s bookworm main\ndeb [check-valid-until=no] http://snapshot.debian.org/archive/debian-security/%s bookworm-security main\n' "$BONFIRE_DEBIAN_SNAPSHOT" "$BONFIRE_DEBIAN_SNAPSHOT" > /etc/apt/sources.list \
	&& apt-get -o Acquire::Check-Valid-Until=false update \
	&& apt-get install -y --no-install-recommends ca-certificates curl libopus0 \
	&& rm -rf /var/lib/apt/lists/* \
	&& mkdir -p /app \
	&& dpkg-query -W -f='${Package}=${Version}\n' | LC_ALL=C sort > /app/release-runtime-packages.txt

WORKDIR /app

COPY --from=build /out/meetingassist /app/meetingassist
COPY --from=build /out/release-build-packages.txt /app/release-build-packages.txt
COPY index.html /app/index.html
COPY public /app/public

EXPOSE 3000/tcp

ENTRYPOINT ["/app/meetingassist", "-addr", ":3000"]
