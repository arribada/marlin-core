FROM debian:stable-20250224@sha256:61b340a0fcbed875ad6d08a3944e9fd0ecb7492f836641cf18726de728e49e7d AS builder

RUN dpkg --add-architecture arm64
RUN apt-get update
RUN apt-get install -y wget make libmnl-dev:arm64 libelf-dev:arm64 crossbuild-essential-arm64

WORKDIR /tmp
RUN wget https://github.com/dosfstools/dosfstools/releases/download/v4.2/dosfstools-4.2.tar.gz
RUN tar xzf dosfstools-4.2.tar.gz
WORKDIR /tmp/dosfstools-4.2
RUN ./configure
RUN make CC=aarch64-linux-gnu-gcc LDFLAGS=-static
RUN make install CC=aarch64-linux-gnu-gcc LDFLAGS=-static

# copy to host with
# docker build --target copier --output=. .
FROM scratch AS copier
COPY --from=builder /usr/local/sbin/fsck.fat /
COPY --from=builder /usr/local/sbin/fatlabel /
COPY --from=builder /usr/local/sbin/mkfs.fat /
