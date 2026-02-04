FROM golang:alpine AS builder

WORKDIR /src

RUN apk add --no-cache git
RUN --mount=type=bind,source=.,target=/src \
    git status && \
    GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-w -s" -o /yaar
    # go get -v && \

FROM scratch
COPY --from=builder /yaar /yaar
COPY web /web
ENTRYPOINT ["/yaar"]
