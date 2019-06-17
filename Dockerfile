FROM golang:1.9

WORKDIR /go/src/kfk
ENV TZ=Asia/Shanghai

RUN ln -snf /usr/share/zoneinfo/$TZ /etc/localtime && echo $TZ > /etc/timezone
ADD . /go/src/kfk
RUN go build ./kfk.go

ENTRYPOINT ["./kfk"]
