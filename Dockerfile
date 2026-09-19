
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/server ./cmd/server

FROM alpine:3.22
WORKDIR /app
COPY --from=build /out/server /app/server
COPY migrations /app/migrations
ENV PORT=8080 DATABASE_PATH=/data/app.json
EXPOSE 8080
CMD ["/bin/sh", "-c", "mkdir -p /data && exec /app/server"]
