FROM oven/bun:1.3.13 AS ui
WORKDIR /ui
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web/ ./
RUN bun run build

FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /ui/dist /src/web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/newapi-ali . && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/newapi-ali /app/newapi-ali
COPY --from=build --chown=65532:65532 /out/data /data
ENV LISTEN_ADDR=0.0.0.0:3000 DATABASE_PATH=/data/ali.db
EXPOSE 3000
ENTRYPOINT ["/app/newapi-ali"]
