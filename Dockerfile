FROM golang:1.26-bookworm AS build

RUN apt-get update \
	&& apt-get install -y --no-install-recommends pkg-config libopus-dev \
	&& rm -rf /var/lib/apt/lists/*

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -o /out/meetingassist .

FROM debian:bookworm-slim

RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates curl libopus0 \
	&& rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=build /out/meetingassist /app/meetingassist
COPY index.html /app/index.html
COPY public /app/public

EXPOSE 3000/tcp

ENTRYPOINT ["/app/meetingassist", "-addr", ":3000"]
