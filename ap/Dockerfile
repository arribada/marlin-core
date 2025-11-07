# docker run -t -i debian:stable-20250224@sha256:61b340a0fcbed875ad6d08a3944e9fd0ecb7492f836641cf18726de728e49e7d
FROM debian:stable-20250224@sha256:61b340a0fcbed875ad6d08a3944e9fd0ecb7492f836641cf18726de728e49e7d AS builder

RUN dpkg --add-architecture arm64
RUN apt-get update
RUN apt-get install -y qemu-user-static golang-go ca-certificates hostapd

WORKDIR /tmp
RUN go install github.com/gokrazy/freeze/cmd/...@latest
RUN ~/go/bin/freeze -wrap=qemu-aarch64-static $(which hostapd)

# copy to host with
# docker build --target copier --output=. .
FROM scratch AS copier
COPY --from=builder /tmp/freeze/ /
