FROM goreleaser/goreleaser-cross:v1.24.6@sha256:81e07633c633260da83369d2fbeb972fb746e5e613c8699cfa41fdec9c21340d AS builder

RUN apt-get update
# unzip needed for ingest
RUN apt-get install -y git

ARG GOPROXY=direct
ENV GOPATH=/home/marlin
RUN go install github.com/gokrazy/tools/cmd/gok@6bec690fe5cd
WORKDIR /home/marlin

# credentials for fetching private (repos) deps.
# N.B: gitcreds is going to leak into the docker image, so beware where we're building that.
ARG gitcreds
COPY ./bin/gitcreds.sh .
RUN ./gitcreds.sh $gitcreds

RUN mkdir -p /home/marlin

WORKDIR /home/marlin/src/github.com/gokrazy/kernel.rpi
COPY ./kernel.rpi .

WORKDIR /home/marlin/src/github.com/arribada/marlin-core/dosfstools
COPY ./dosfstools .
RUN make fsck.fat

WORKDIR /home/marlin/src/github.com/arribada/marlin-core/usbnet
COPY ./usbnet .

WORKDIR /home/marlin/src/github.com/arribada/marlin-core/ffprobe
COPY ./ffprobe .
RUN make ffprobe

WORKDIR /home/marlin/src/github.com/arribada/marlin-core/syslogd
COPY ./syslogd .

WORKDIR /home/marlin/src/github.com/arribada/marlin-core/ap
COPY ./ap .

WORKDIR /home/marlin/src/github.com/arribada/marlin-core/cellular
COPY ./cellular .

WORKDIR /home/marlin/src/github.com/arribada/marlin-core/gateway
COPY ./gateway .

WORKDIR /home/marlin/src/github.com/arribada/marlin-core/common
COPY ./common .

WORKDIR /home/marlin/src/github.com/arribada/marlin-core/gengps
COPY ./gengps .

WORKDIR /home/marlin/src/github.com/arribada/marlin-core/captain
COPY ./captain .

WORKDIR /home/marlin/src/github.com/arribada/marlin-core/databrowser
COPY ./databrowser .

WORKDIR /home/marlin/src/github.com/arribada/marlin-core/gc
COPY ./gc .

WORKDIR /home/marlin/src/github.com/arribada/marlin-core/transcriber
COPY ./transcriber .
RUN cd ./whisper.cpp/bindings/go && make whisper
# if we let that be implicitly done during gok update, then whisper.h is not found during the compilation of the bindings.
# while if we do it here and now, no problem. So it's necessary to keep it until we understand better what's going on.
RUN make build

WORKDIR /home/marlin/src/github.com/arribada/marlin-core/collector
COPY ./collector .
RUN make build SUDO=""

ARG instance=doesnotexist
RUN echo "building for "$instance
WORKDIR /home/marlin/src/github.com/arribada/marlin-core/gokrazy/$instance
# lowkey check that $instance is a valid one.
# only copy our instance (and not the whole gokrazy dir), so that
# we stay cached if another instance config changes
COPY ./gokrazy/$instance .
COPY ./gokrazy/secrets .
# if we want to do by host: it would be in /root/.config/gokrazy/hosts/thehostname
WORKDIR /root/.config/gokrazy
COPY ./gokrazy/secrets/http-password.txt .

# keep it as last rule, so we are in right place for other stages
WORKDIR /home/marlin/src/github.com/arribada/marlin-core
COPY ./Makefile .

# run all tests
RUN make tests

FROM builder AS gengaf
ARG instance=doesnotexist
RUN --mount=type=cache,target=/home/marlin/pkg INSTANCE=$instance make gaf2

FROM builder AS gensbom
ARG instance=doesnotexist
RUN --mount=type=cache,target=/home/marlin/pkg INSTANCE=$instance make sbom2

# To generate full.gaf, and then copy it to host with:
# docker build --target gaf --output=. .
FROM scratch AS gaf
ARG instance=doesnotexist
COPY --from=gengaf /tmp/$instance.gaf /

FROM scratch AS sbom
ARG instance=doesnotexist
COPY --from=gensbom /tmp/$instance.json /

FROM scratch AS builddir
ARG instance=doesnotexist
COPY --from=gengaf /home/marlin/src/github.com/arribada/marlin-core/gokrazy/$instance/builddir /

FROM builder AS forceupdate
ARG instance=doesnotexist
# only works at build time with non-ts IP
RUN INSTANCE=$instance make forceupdate2
# tailscale hosts are not resolved, so only works for now if update has been configured by IP
# ENTRYPOINT ["make", "forceupdate2"]

FROM scratch AS artifact
COPY --from=builder /tmp/freeze /

