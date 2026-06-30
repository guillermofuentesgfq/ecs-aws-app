# ecs-aws-app

[![Go](https://img.shields.io/badge/Go-%3E%3D1.22-00ADD8?logo=go)](https://go.dev/)
[![Docker](https://img.shields.io/badge/Docker-multi--stage-2496ED?logo=docker)](https://www.docker.com/)
[![X-Ray](https://img.shields.io/badge/AWS-X--Ray-FF9900?logo=amazon-aws)](https://aws.amazon.com/xray/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Go application for ECS Fargate with RDS PostgreSQL connectivity, X-Ray distributed tracing, and structured logging.

This is the application companion to the [ecs-aws-infra](https://github.com/guillermofuentesgfq/ecs-aws-infra) infrastructure repository.

## Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Lightweight L4 health check — returns `{"status": "ok"}` with no external dependencies (used by ALB target group) |
| `GET` | `/ready` | Readiness probe — executes `SELECT 1` against RDS; returns 200 if healthy, 503 if degraded (used by CodeDeploy `BeforeAllowTraffic` hook) |
| `GET` | `/api/db` | Database query endpoint — runs `SELECT 1` and returns latency in milliseconds with X-Ray tracing |

## Architecture

```
                ┌──────────────┐
                │    ALB/ELB   │
                │  :8080/health│
                └──────┬───────┘
                       │
                ┌──────▼───────┐
                │   ECS Task   │
                │ ┌──────────┐ │
                │ │   App    │ │─── X-Ray daemon (sidecar)
                │ │ :8080    │ │
                │ └────┬─────┘ │
                │      │       │
                └──────┼───────┘
                       │
                ┌──────▼───────┐
                │     RDS      │
                │  PostgreSQL  │
                └──────────────┘
```

## Endpoint Details

### `/health`
Returns immediately without checking any dependency. Used by the ALB target group for basic liveness detection.

### `/ready`
Verifies the application can reach the database. Used by CodeDeploy's `BeforeAllowTraffic` hook during canary deployments to prevent traffic routing to tasks that can't serve requests.

### `/api/db`
Performs a `SELECT 1` query against RDS and returns the measured latency. Wrapped with an X-Ray subsegment for tracing.

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | HTTP server port |
| `DB_HOST` | — | RDS PostgreSQL hostname |
| `DB_PORT` | `5432` | RDS PostgreSQL port |
| `DB_NAME` | — | Database name |
| `DB_USER` | — | Database user |
| `DB_PASSWORD` | — | Database password |
| `DB_SSLMODE` | `require` | PostgreSQL SSL/TLS mode |
| `AWS_XRAY_DAEMON_ADDRESS` | `0.0.0.0:2000` | X-Ray daemon endpoint (sidecar container) |

## CI/CD Integration

The application is deployed via CodePipeline (defined in the infra repo). On each push to `main`:

1. **CodeBuild** executes `buildspec.yml`: builds the Go binary, creates the Docker image, and pushes it to ECR
2. **CodeDeploy** performs an ECS blue/green deployment with canary traffic shifting (`10% for 5 minutes → 100%`)
3. **BeforeAllowTraffic** hook calls the `/ready` endpoint to validate the new task can connect to RDS before serving traffic

## Local Development

```bash
# Build the binary
go build -o bin/app .

# Run locally (set env vars for RDS access)
export DB_HOST=localhost DB_PORT=5432 DB_NAME=mydb DB_USER=user DB_PASSWORD=pass
./bin/app

# Build Docker image
docker build -t ecs-aws-app .

# Run container
docker run -p 8080:8080 ecs-aws-app
```

## Deployment

See the [ecs-aws-infra](https://github.com/guillermofuentesgfq/ecs-aws-infra) repository for infrastructure deployment instructions.

## License

MIT
