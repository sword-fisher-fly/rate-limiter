ARG ARCHITECTURE=linux/amd64

FROM --platform=${ARCHITECTURE} swr.cn-east-3.myhuaweicloud.com/erlang-ci/golang-1.27-alpine:v1.27.3 AS builder

WORKDIR /src
COPY . .

# Build go binary. CGO is off so the result is a static binary that runs on
# the plain alpine runtime image without a libc.
ENV GOPROXY=https://goproxy.cn,direct
ENV CGO_ENABLED=0
RUN mkdir -p bin && go build -o bin/rate-limiter ./cmd/rate-limiter

# Runtime stage
FROM --platform=${ARCHITECTURE} swr.cn-east-3.myhuaweicloud.com/erlang-ci/alpine:3.20

# tzdata: the app formats release plan ids and log timestamps in local time,
# and the mysql dsn reads datetimes with loc=Local. Alpine ships no zone
# database, so without this the container would run in UTC.
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.huaweicloud.com/g' /etc/apk/repositories && \
    apk add --no-cache tzdata

# Unprivileged account the api runs as. The uid / gid are fixed so a
# deployment can pin them (runAsUser / runAsGroup / fsGroup) without having
# to look them up.
RUN addgroup -g 10001 appuser && \
    adduser -D -H -u 10001 -G appuser -s /sbin/nologin appuser

WORKDIR /app

# Copy binary from builder
COPY --from=builder /src/bin/ ./
COPY --from=builder /src/etc/config.yaml ./etc/config.yaml

# The migrations hold the sql files that create the tables of the database,
# they are applied on startup and the directory is relative to the working
# directory of the process.
COPY --from=builder /src/migrations/ ./migrations/

# The log directory is the one place the api writes to. The binary itself
# stays owned by root, so the unprivileged user cannot replace it.
RUN mkdir -p /app/logs && \
    chown appuser:appuser /app /app/etc && \
    chown -R appuser:appuser /app/logs

ENV TZ=Asia/Shanghai

USER appuser

EXPOSE 10088

ENTRYPOINT ["/app/rate-limiter", "-config", "/app/etc/config.yaml"]
