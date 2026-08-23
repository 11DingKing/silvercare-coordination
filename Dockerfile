FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOTOOLCHAIN=local go build -trimpath -o /out/silvercare-server ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && addgroup -S app && adduser -S -G app app
WORKDIR /app
COPY --from=build /out/silvercare-server /usr/local/bin/silvercare-server
RUN mkdir -p /app/data && chown -R app:app /app
USER app
ENV SILVERCARE_ADDR=:8080 SILVERCARE_DATABASE=/app/data/silvercare.db
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/silvercare-server"]
