FROM golang:1.25.7-alpine3.23 as build
ENV GO111MODULE=on
ENV CGO_ENABLED=0
ENV GOOS=linux

RUN apk add --no-cache make git

WORKDIR /go/src/github.com/supatype/auth

# Pulling dependencies
COPY ./Makefile ./go.* ./
RUN make deps

# Building stuff
COPY . /go/src/github.com/supatype/auth

# Make sure you change the RELEASE_VERSION value before publishing an image.
RUN RELEASE_VERSION=unspecified make build

# Always use alpine:3 so the latest version is used. This will keep CA certs more up to date.
FROM alpine:3
RUN adduser -D -u 1000 supabase

RUN apk add --no-cache ca-certificates
COPY --from=build /go/src/github.com/supatype/auth/supatype-server /usr/local/bin/supatype-server
COPY --from=build /go/src/github.com/supatype/auth/migrations /usr/local/etc/auth/migrations/
RUN ln -sf /usr/local/bin/supatype-server /usr/local/bin/auth \
 && ln -sf /usr/local/bin/supatype-server /usr/local/bin/gotrue

ENV GOTRUE_DB_MIGRATIONS_PATH /usr/local/etc/auth/migrations

USER supabase
CMD ["supatype-server"]
