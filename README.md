# ecs-aws-app

Go application for ECS Fargate deployment with AWS X-Ray tracing.

## Endpoints

| Method | Path      | Description                        |
|--------|-----------|------------------------------------|
| GET    | `/health`   | L4 health check (no dependencies)  |
| GET    | `/ready`    | Readiness probe (checks RDS query) |
| GET    | `/api/db`   | RDS query with latency             |

## Environment Variables

| Variable                     | Default           | Description                     |
|------------------------------|-------------------|---------------------------------|
| `PORT`                         | `8080`              | HTTP server port                |
| `DB_HOST`                      | —                  | RDS hostname                    |
| `DB_PORT`                      | `5432`              | RDS port                        |
| `DB_NAME`                      | —                  | Database name                   |
| `DB_USER`                      | —                  | Database user                   |
| `DB_PASSWORD`                  | —                  | Database password               |
| `DB_SSLMODE`                   | `require`           | PostgreSQL SSL mode             |
| `AWS_XRAY_DAEMON_ADDRESS`      | `0.0.0.0:2000`       | X-Ray daemon address            |

## Build

```bash
go build -o bin/app .
```

## Docker

```bash
docker build -t ecs-aws-app .
docker run -p 8080:8080 ecs-aws-app
```
