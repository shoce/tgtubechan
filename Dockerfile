FROM golang:1.21.0 as build
WORKDIR /root/
RUN apt update
RUN apt -y -q install xz-utils
RUN curl -s -S -L -O https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-amd64-static.tar.xz
RUN ls -l -a
RUN tar -x -J -f ffmpeg-release-amd64-static.tar.xz
RUN ls -l -a
RUN mkdir -p /root/tgytchan/
COPY tgytchan.go go.mod go.sum /root/tgytchan/
RUN mv /root/ffmpeg-*-amd64-static/ffmpeg /root/tgytchan/ffmpeg
RUN /root/tgytchan/ffmpeg -version
WORKDIR /root/tgytchan/
RUN go version
RUN go get -a -u -v
RUN ls -l -a
RUN go build -o tgytchan tgytchan.go
RUN ls -l -a


FROM alpine:3.18.0
RUN apk add --no-cache tzdata
RUN apk add --no-cache gcompat && ln -s -f -v ld-linux-x86-64.so.2 /lib/libresolv.so.2
RUN mkdir -p /opt/tgytchan/
COPY --from=build /root/tgytchan/ffmpeg /opt/tgytchan/ffmpeg
COPY --from=build /root/tgytchan/tgytchan /opt/tgytchan/tgytchan
RUN ls -l -a /opt/tgytchan/
WORKDIR /opt/tgytchan/
ENTRYPOINT ["./tgytchan"]

