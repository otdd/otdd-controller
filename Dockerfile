FROM golang
RUN mkdir -p /go/src/k8s.io/otdd-controller
ADD . /go/src/k8s.io/otdd-controller
WORKDIR /go
RUN go get ./...
RUN go install -v ./...
CMD ["/go/bin/otdd-controller"]
