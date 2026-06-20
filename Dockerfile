FROM golang:1.26-bookworm AS build

RUN apt-get update \
	&& apt-get install -y --no-install-recommends pkg-config libopus-dev \
	&& rm -rf /var/lib/apt/lists/*

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -o /out/meetingassist .

FROM debian:bookworm-slim AS meetingassist-runtime

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

RUN apt-get update \
	&& apt-get install -y --no-install-recommends bubblewrap ca-certificates curl git libopus0 nodejs npm \
	&& npm install -g @openai/codex \
	&& rm -rf /var/lib/apt/lists/* /root/.npm

WORKDIR /workspace/meetingassist

COPY --from=build /out/meetingassist /app/meetingassist

ENTRYPOINT ["/app/meetingassist", "-codex-runner"]

FROM meetingassist-runtime
