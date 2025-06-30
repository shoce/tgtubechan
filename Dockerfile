

# https://hub.docker.com/_/golang/tags
FROM golang:1.24.4-alpine3.22 AS build
ENV CGO_ENABLED=0

#ARG TARGETARCH
#
#RUN apt update
#RUN apt -y -q install xz-utils
#
#RUN mkdir -p /root/ffmpeg/
#WORKDIR /root/ffmpeg/
#RUN curl -s -S -L -O https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-$TARGETARCH-static.tar.xz
#RUN tar -x -J -f ffmpeg-release-$TARGETARCH-static.tar.xz
#RUN mv ffmpeg-*-static/ffmpeg ffmpeg
#RUN ls -l -a
#RUN ./ffmpeg -version

RUN mkdir -p /root/tgtubechan/
WORKDIR /root/tgtubechan/
COPY tgtubechan.go go.mod go.sum /root/tgtubechan/
RUN go version
RUN go get -v
RUN go build -o tgtubechan tgtubechan.go
RUN ls -l -a


# https://hub.docker.com/_/alpine/tags
FROM alpine:3.22
RUN apk add --no-cache tzdata
RUN apk add --no-cache gcompat && ln -s -f -v ld-linux-x86-64.so.2 /lib/libresolv.so.2

#COPY --from=build /root/tgtubechan/ffmpeg /bin/
COPY --from=build /root/tgtubechan/tgtubechan /bin/

ENTRYPOINT ["/bin/tgtubechan"]


