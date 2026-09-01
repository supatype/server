FROM golang:1.25.13-alpine3.23 AS build
ENV GO111MODULE=on
ENV CGO_ENABLED=0
ENV GOOS=linux

RUN apk add --no-cache make git

WORKDIR /go/src/github.com/supatype/server

# Pulling dependencies
COPY ./Makefile ./go.* ./
RUN make deps

# Building stuff
COPY . /go/src/github.com/supatype/server

# Make sure you change the RELEASE_VERSION value before publishing an image.
RUN RELEASE_VERSION=unspecified make build

# Always use alpine:3 so the latest version is used. This will keep CA certs more up to date.
FROM alpine:3
RUN adduser -D -u 1000 supatype

RUN apk add --no-cache ca-certificates
COPY --from=build /go/src/github.com/supatype/server/supatype-server /usr/local/bin/supatype-server
COPY --from=build /go/src/github.com/supatype/server/migrations /usr/local/etc/auth/migrations/
# The old name, kept as a symlink: an image tag is not the only thing that names
# this binary, and a deployment carrying `command: auth` should not stop working
# on the version that renamed it. The trailing backslash this line used to end
# with continued the command into two blank lines, which Docker warns about as
# NoEmptyContinuation.
RUN ln -sf /usr/local/bin/supatype-server /usr/local/bin/auth

ENV SUPATYPE_DB_MIGRATIONS_PATH=/usr/local/etc/auth/migrations

USER supatype
CMD ["supatype-server"]
