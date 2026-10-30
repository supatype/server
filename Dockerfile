FROM golang:1.26.10-alpine3.23 AS build
ENV GO111MODULE=on
ENV CGO_ENABLED=0
ENV GOOS=linux

RUN apk add --no-cache make git

WORKDIR /go/src/github.com/supatype/server

# Pulling dependencies
COPY ./Makefile ./go.* ./
# go.mod replaces gobuffalo/envy with this directory, so it has to be here before the download.
COPY ./third_party ./third_party
RUN make deps

# Building stuff
COPY . /go/src/github.com/supatype/server

# Make sure you change the RELEASE_VERSION value before publishing an image.
# RELEASE_PUBLIC_KEY is the minisign key plugin widgets must be signed with; the publish
# workflows pass it (hack/release-public-key.sh). Left empty, the image serves no plugin
# widget outside dev mode.
ARG RELEASE_PUBLIC_KEY=
RUN RELEASE_VERSION=unspecified RELEASE_PUBLIC_KEY="$RELEASE_PUBLIC_KEY" make build

# Always use alpine:3 so the latest version is used. This will keep CA certs more up to date.
FROM alpine:3

# The server's own licence. The base image's packages carry their own; this
# label and /LICENSE describe the server binary, so they ship with every build
# of the image rather than only with the release workflow's.
LABEL org.opencontainers.image.licenses="MIT"

RUN adduser -D -u 1000 supatype

RUN apk add --no-cache ca-certificates
COPY --chmod=0644 LICENSE /LICENSE
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
