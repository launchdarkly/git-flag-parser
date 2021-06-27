FROM alpine:3.13.5

RUN apk update
RUN apk add --no-cache git
RUN apk add --no-cache openssh

COPY ld-find-code-refs /usr/local/bin/ld-find-code-refs

ENTRYPOINT ["ld-find-code-refs"]
