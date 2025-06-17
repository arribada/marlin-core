FROM debian:stable-20250224@sha256:61b340a0fcbed875ad6d08a3944e9fd0ecb7492f836641cf18726de728e49e7d AS builder

RUN apt-get update
RUN apt-get install -y wget xz-utils

WORKDIR /tmp
# release 7.0.2 , as of 2025-06-17
# we could do it from source, but this seems trustworthy enough for now.
ENV HASH=807afe21601db0a73e426121c7d636ea
RUN wget https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-arm64-static.tar.xz
RUN test "$HASH" = "$(md5sum ffmpeg-release-arm64-static.tar.xz | awk '{print $1}')"
RUN tar xf ffmpeg-release-arm64-static.tar.xz

# copy to host with
# docker build --target copier --output=. .
FROM scratch AS copier
COPY --from=builder /tmp/ffmpeg-7.0.2-arm64-static/ffprobe /
