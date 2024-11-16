

# https://hub.docker.com/_/golang/tags
FROM golang:1.23.2 AS build
ARG TARGETARCH
WORKDIR /root/

RUN apt update
RUN apt -y -q install xz-utils
RUN curl -s -S -L -O https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-$TARGETARCH-static.tar.xz
RUN ls -l -a
RUN tar -x -J -f ffmpeg-release-$TARGETARCH-static.tar.xz
RUN ls -l -a

RUN mkdir -p /root/tgtubechan/
WORKDIR /root/tgtubechan/

RUN mv /root/ffmpeg-*-static/ffmpeg /root/tgtubechan/ffmpeg
RUN /root/tgtubechan/ffmpeg -version
COPY tgtubechan.go go.mod go.sum /root/tgtubechan/
RUN go version
RUN go get -v
RUN go build -o tgtubechan tgtubechan.go
RUN ls -l -a


# https://hub.docker.com/_/alpine/tags
FROM alpine:3.20.3
RUN apk add --no-cache tzdata
RUN apk add --no-cache gcompat && ln -s -f -v ld-linux-x86-64.so.2 /lib/libresolv.so.2
COPY --from=build /root/tgtubechan/ffmpeg /bin/ffmpeg
COPY --from=build /root/tgtubechan/tgtubechan /bin/tgtubechan
RUN ls -l -a /bin/ffmpeg /bin/tgtubechan
ENTRYPOINT ["/bin/tgtubechan"]


