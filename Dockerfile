FROM golang:1.22-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build -o /app -ldflags="-s -w" .

# ---

FROM gcr.io/distroless/static-debian12

COPY --from=builder /app /app

USER 65534

EXPOSE 8080

HEALTHCHECK CMD ["/app"]

LABEL org.opencontainers.image.source="https://github.com/guillermofuentesgfq/ecs-aws-app"

ENTRYPOINT ["/app"]
