FROM golang:1.26-bookworm AS build

ARG BONFIRE_RELEASE_COMMIT=unqualified
ARG BONFIRE_GIT_TREE_DIGEST=unqualified
ARG BONFIRE_BUILD_CONFIG_SHA256=unqualified
ARG BONFIRE_BUILD_TRANSITIVE_INPUTS_SHA256=unqualified
ARG BONFIRE_SOURCE_ARCHIVE_SHA256=unqualified

RUN apt-get update \
	&& apt-get install -y --no-install-recommends pkg-config libopus-dev \
	&& rm -rf /var/lib/apt/lists/*

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags "-X main.mediaSoakBuildCommit=${BONFIRE_RELEASE_COMMIT} -X main.mediaSoakBuildTreeDigest=${BONFIRE_GIT_TREE_DIGEST} -X main.mediaSoakBuildConfigDigest=${BONFIRE_BUILD_CONFIG_SHA256} -X main.mediaSoakBuildInputsDigest=${BONFIRE_BUILD_TRANSITIVE_INPUTS_SHA256} -X main.mediaSoakBuildSourceArchiveDigest=${BONFIRE_SOURCE_ARCHIVE_SHA256}" -o /out/meetingassist .

FROM debian:bookworm-slim AS meetingassist-runtime

ARG BONFIRE_RELEASE_COMMIT=unqualified
ARG BONFIRE_GIT_TREE_DIGEST=unqualified
ARG BONFIRE_BUILD_CONFIG_SHA256=unqualified
ARG BONFIRE_BUILD_TRANSITIVE_INPUTS_SHA256=unqualified
ARG BONFIRE_SOURCE_ARCHIVE_SHA256=unqualified

LABEL org.opencontainers.image.revision="${BONFIRE_RELEASE_COMMIT}" \
      xyz.thebonfire.git-tree-digest="${BONFIRE_GIT_TREE_DIGEST}" \
      xyz.thebonfire.config-digest="${BONFIRE_BUILD_CONFIG_SHA256}" \
      xyz.thebonfire.transitive-inputs-digest="${BONFIRE_BUILD_TRANSITIVE_INPUTS_SHA256}" \
      xyz.thebonfire.source-archive-digest="${BONFIRE_SOURCE_ARCHIVE_SHA256}"

RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates curl libopus0 \
	&& rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=build /out/meetingassist /app/meetingassist
COPY index.html /app/index.html
COPY public /app/public

EXPOSE 3000/tcp

ENTRYPOINT ["/app/meetingassist", "-addr", ":3000"]

FROM debian:bookworm-slim AS codex-runner

ARG CODEX_CLI_VERSION=0.144.1

RUN apt-get update \
	&& apt-get install -y --no-install-recommends bubblewrap ca-certificates curl git libopus0 nodejs npm \
	&& npm install -g "@openai/codex@${CODEX_CLI_VERSION}" \
	&& rm -rf /var/lib/apt/lists/* /root/.npm

WORKDIR /workspace/meetingassist

COPY --from=build /out/meetingassist /app/meetingassist

ENTRYPOINT ["/app/meetingassist", "-codex-runner"]

FROM meetingassist-runtime
