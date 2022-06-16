FROM golang:latest 

WORKDIR /go/sipgo

COPY . . 

RUN go mod download